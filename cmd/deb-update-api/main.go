package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var (
	aptRunning   bool
	aptLastCheck time.Time
	updLastCheck time.Time
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	data, err := ioutil.ReadFile("")
	if err != nil {
		panic(err)
	}

	cfg := config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		panic(err)
	}

	api := gin.Default()

	svr := server{cfg: cfg, statusEvents: make(chan event, len(cfg.Packages))}
	svr.AttachRoutes(api)

	go updateAptCache(func() {
		svr.checkForUpdateablePackages()
		if err := autoUpdatePackages(cfg.AutoUpdateables()...); err != nil {
			log.Println("ERROR: failed to auto update packages:", err)
		}

		for _, pkg := range cfg.Updateable() {
			svr.sendStatusEvent(pkg.Name, "update_available", pkg.Version)
		}
	})

	svr.checkForUpdateablePackages()
	api.Run(":8020")
}

type jsonWriter interface {
	WriteJSON(interface{}) error
}

type server struct {
	cfg          config
	wsconns      []jsonWriter
	statusEvents chan event
}

type event struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

type aptpkg struct {
	Name             string `json:"name"`
	NiceName         string `json:"name2"`
	Auto             bool   `json:"auto"`
	Version          string `json:"version"`
	UpdateAvailable  bool   `json:"update_available"`
	AvailableVersion string `json:"available_version"`
}

type config struct {
	Packages []aptpkg
}

func (cfg config) HasPackage(name string) bool {
	for _, pkg := range cfg.Packages {
		if pkg.Name == name {
			return true
		}
	}
	return false
}

func (cfg config) Updateable() []aptpkg {
	pkgs := []aptpkg{}
	for _, pkg := range cfg.Packages {
		if pkg.UpdateAvailable {
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs
}

func (cfg config) AutoUpdateables() (names []string) {
	for _, pkg := range cfg.Packages {
		if pkg.Auto {
			names = append(names, pkg.Name)
		}
	}
	return
}

func (svr server) sendStatusEvent(name, status, version string) {
	if len(svr.statusEvents) == cap(svr.statusEvents) {
		return
	}
	if len(svr.wsconns) == 0 {
		return
	}
	svr.statusEvents <- event{name, status, version}
}

func (svr server) AttachRoutes(r gin.IRouter) {
	r.GET("/packages/status", svr.statusUpdates)
	r.GET("/packages", svr.list)
	r.PUT("/packages", svr.install)
	r.PUT("/packages/all", svr.installAll)
}

func (svr server) updateStatuses(c *gin.Context) {
	for {
		ev := <-svr.statusEvents
		if len(svr.wsconns) == 0 {
			time.Sleep(time.Second)
		}

		for _, conn := range svr.wsconns {
			if err := conn.WriteJSON(ev); err != nil {
				log.Println("error write json: ", err)
			}
		}
	}
}

func (svr server) statusUpdates(c *gin.Context) {
	// Upgrade get request to webSocket protocol
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("error get connection")
		log.Fatal(err)
	}
	// defer ws.Close()
	svr.wsconns = append(svr.wsconns, ws)

}

func (svr server) installAll(c *gin.Context) {
	names := []string{}
	for _, pkg := range svr.cfg.Packages {
		if pkg.UpdateAvailable {
			names = append(names, pkg.Name)
		}
	}

	if err := installPackage(names...); err != nil {
		c.AbortWithError(500, err)
		return
	}

	c.Status(204)
}

func (svr server) install(c *gin.Context) {
	names := strings.Split(c.Query("names"), ",")

	for _, name := range names {
		if !svr.cfg.HasPackage(name) {
			c.AbortWithStatus(403)
			return
		}
	}

	for _, name := range names {
		svr.sendStatusEvent(name, "updating", "")
	}

	if err := installPackage(names...); err != nil {
		for _, name := range names {
			svr.sendStatusEvent(name, "update_failed", "")
		}
		c.AbortWithError(500, err)
		return
	}

	for _, name := range names {
		ver, _, _ := packageHasUpdate(name)
		svr.sendStatusEvent(name, "updated", ver)
	}

	c.Status(200)
}

func (svr server) list(c *gin.Context) {
	pkgs := []aptpkg{}
	upkgs := []aptpkg{}

	for _, pkg := range svr.cfg.Packages {
		if pkg.UpdateAvailable {
			upkgs = append(upkgs, pkg)
		} else {
			pkgs = append(pkgs, pkg)
		}
	}

	c.JSON(200, map[string]interface{}{
		"updateable": upkgs,
		"current":    pkgs,
	})
}

func (svr *server) checkForUpdateablePackages() {
	for _, pkg := range svr.cfg.Packages {
		pkg.Version, pkg.AvailableVersion, pkg.UpdateAvailable = packageHasUpdate(pkg.Name)
	}
}

func packageHasUpdate(name string) (string, string, bool) {
	cmd := exec.Command("bash", "-c", `apt-cache policy `+name+` | grep -P "Installed|Candidate" `)
	data, err := cmd.Output()
	if err != nil {
		log.Println("ERROR: failed to detect package version for", name, ":", err)
		return "", "", false
	}

	lines := strings.Split(string(data), "\n")
	availVersion := strings.Split(lines[0], ": ")[1]
	version := strings.Split(lines[1], ": ")[1]

	return version, availVersion, availVersion != version
}

// Check if package manager is running (every 5 seconds).
func isAptRunning() bool {
	if time.Since(aptLastCheck) >= 5*time.Second {
		aptRunning = false

		dir, err := ioutil.ReadDir("/proc")
		if err != nil {
			log.Fatal(err)
		}

		reApt := regexp.MustCompile(`(?m:^(apt-get|dselect|aptitude)$)`)
		reProcess := regexp.MustCompile(`^\d+$`)

		for _, v := range dir {
			if !v.IsDir() {
				continue
			}

			if !reProcess.MatchString(v.Name()) {
				continue
			}

			comm, err := ioutil.ReadFile(path.Join("/proc", v.Name(), "comm"))
			if err != nil {
				continue
			}

			if reApt.Match(comm) {
				aptRunning = true
				updLastCheck = time.Time{}
				break
			}
		}

		aptLastCheck = time.Now()
	}

	return aptRunning
}

func updateAptCache(after func()) {
	for {
		if isAptRunning() {
			time.Sleep(5 * time.Second)
			continue
		}

		time.Sleep(5 * time.Minute)
		cmd := exec.Command("/usr/bin/apt-get", "update")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err == nil {
			log.Printf("ERROR: updating apt cache: %s", err)
		}

		after()
	}
}

func autoUpdatePackages(names ...string) error {
	return installPackage(names...)
}

func installPackage(names ...string) error {
	cmd := exec.Command("apt-get", "install", "-y")
	os.Args = append(os.Args, names...)
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
