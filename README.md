# deb-update-api
Track specified packages and update them via an API.  It is intended to be embedded in a control panel that allows whitelisted packages to be updated automatically or by the end user.

## Usage

### Config

```
---
packages:
  - name: linux-image
    name2: OS Kernel
    auto: false
  - name: git
    auto: true
```

### API

Call this to get a list of the packages and their current status.

```
GET /packages

{
  "updateable": [
    {
      "name": "linux-image",
      "name2": "OS Kernel",
      "version": "5.7.6",
      "update_available": true,
      "available_version": "5.7.7",
      "auto": false
    }
  ],
  "current": [
    {
      "name": "git",
      "version": "1.2.3",
      "update_available": false,
      "available_version": "",
      "auto": true
    }
  ]
}
```

Then you can update specific packages like so:

```
PUT /packages?names=git,linux-image
```

Or you can update all of the packages:

```
PUT /packages/all
```

You can listen via websockets for package update statuses:

```
GET /packages/status

{"name": "linux-image", "status": "update_queued"}
{"name": "linux-image", "status": "updating"}
{"name": "linux-image", "status": "update_failed"}
{"name": "linux-image", "status": "update_queued"}
{"name": "linux-image", "status": "updating"}
{"name": "linux-image", "status": "updated", "version": "1.2.3"}
```

## Todo

- [x] apt-get update
- [x] auto update functionality
- [x] list packages
- [x] update specified packages
- [x] update all packages
- [ ] websocket channel
