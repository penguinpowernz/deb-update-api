package main

import (
	"errors"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var ErrBlacklistedPackage = errors.New("blacklisted package")

var (
	aptRunning   bool
	aptLastCheck time.Time
	updLastCheck time.Time
)

func main() {

	api := gin.Default()

	s := server{}
	s.AttachRoutes(api)

	go func() {
		for {
			if isAptRunning() {
				time.Sleep(5 * time.Second)
				continue
			}

			time.Sleep(5 * time.Minute)
			cmd := exec.Command("/usr/bin/apt-get", "update")
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err == nil {
				log.Printf("ERROR: %s", err)
			}
		}
	}()

	api.Run(":8020")
}

type server struct {
	cfg config
}

type config struct {
	SearchList       []string
	BlackList        []string
	CanInstall       bool
	NicePackageNames map[string]string
}

func (svr server) AttachRoutes(r gin.IRouter) {
	r.POST("/install", svr.install)
	r.GET("/list", svr.listUpdates)
}

func (svr server) install(c *gin.Context) {
	if !svr.cfg.CanInstall {
		c.AbortWithStatus(403)
		return
	}

	pkgs := svr.packagesWithUpdates()
	for _, pkg := range pkgs {
		if err := svr.installPackage(pkg[0]); err != nil && err != ErrBlacklistedPackage {
			c.AbortWithError(500, err)
		}
	}

	c.Status(200)
}

func (svr server) listUpdates(c *gin.Context) {
	pkgs := svr.packagesWithUpdates()
	c.JSON(299, pkgs)
}

func (svr server) installPackage(name string) error {
	for _, pkg := range svr.cfg.BlackList {
		if pkg == name {
			return ErrBlacklistedPackage
		}
	}

	var found bool
	for _, pkg := range svr.cfg.SearchList {
		if pkg == name {
			found = true
		}
	}

	if !found {
		return ErrBlacklistedPackage
	}

	cmd := exec.Command("apt-get", "install", "-y", name)
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func (svr server) packagesWithUpdates() (pkgs [][]string) {
	for _, pkg := range svr.cfg.SearchList {
		v, yes := packageHasUpdate(pkg)
		if yes {
			pkgs = append(pkgs, []string{svr.nicePackageName(pkg), v})
		}
	}
	return
}

func (svr server) nicePackageName(n string) string {
	npn, found := svr.cfg.NicePackageNames[n]
	if found {
		return npn
	}
	return n
}

func packageHasUpdate(name string) (string, bool) {
	cmd := exec.Command("bash", "-c", `apt-cache policy signal-desktop|grep -A 1 "Version table"|grep "***"`)
	data, err := cmd.Output()
	if err != nil {
		return "", false
	}

	bits := strings.Split(string(data), " ")
	if len(bits) < 3 {
		return "", false
	}

	return bits[2], true
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
