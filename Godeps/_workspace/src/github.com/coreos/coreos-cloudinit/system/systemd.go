package system

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/coreos/nova-agent-watcher/Godeps/_workspace/src/github.com/coreos/coreos-cloudinit/third_party/github.com/coreos/go-systemd/dbus"
)

// fakeMachineID is placed on non-usr CoreOS images and should
// never be used as a true MachineID
const fakeMachineID = "42000000000000000000000000000042"

type Unit struct {
	Name    string
	Runtime bool
	Content string
}

func (u *Unit) Type() string {
	ext := filepath.Ext(u.Name)
	return strings.TrimLeft(ext, ".")
}

func (u *Unit) Group() (group string) {
	t := u.Type()
	if t == "network" || t == "netdev" || t == "link" {
		group = "network"
	} else {
		group = "system"
	}
	return
}

type Script []byte

func PlaceUnit(u *Unit, root string) (string, error) {
	dir := "etc"
	if u.Runtime {
		dir = "run"
	}

	dst := path.Join(root, dir, "systemd", u.Group())
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.MkdirAll(dst, os.FileMode(0755)); err != nil {
			return "", err
		}
	}

	dst = path.Join(dst, u.Name)

	file := File{
		Path:               dst,
		Content:            u.Content,
		RawFilePermissions: "0644",
	}

	err := WriteFile(&file)
	if err != nil {
		return "", err
	}

	return dst, nil
}

func EnableUnitFile(file string, runtime bool) error {
	conn, err := dbus.New()
	if err != nil {
		return err
	}

	files := []string{file}
	_, _, err = conn.EnableUnitFiles(files, runtime, true)
	return err
}

func separateNetworkUnits(units []Unit) ([]Unit, []Unit) {
	networkUnits := make([]Unit, 0)
	nonNetworkUnits := make([]Unit, 0)
	for _, unit := range units {
		if unit.Group() == "network" {
			networkUnits = append(networkUnits, unit)
		} else {
			nonNetworkUnits = append(nonNetworkUnits, unit)
		}
	}
	return networkUnits, nonNetworkUnits
}

func StartUnits(units []Unit) error {
	networkUnits, nonNetworkUnits := separateNetworkUnits(units)
	if len(networkUnits) > 0 {
		if err := RestartUnitByName("systemd-networkd.service"); err != nil {
			return err
		}
	}

	for _, unit := range nonNetworkUnits {
		if err := RestartUnitByName(unit.Name); err != nil {
			return err
		}
	}

	return nil
}

func DaemonReload() error {
	conn, err := dbus.New()
	if err != nil {
		return err
	}

	_, err = conn.Reload()
	return err
}

func RestartUnitByName(name string) error {
	log.Printf("Restarting unit %s", name)
	conn, err := dbus.New()
	if err != nil {
		return err
	}

	output, err := conn.RestartUnit(name, "replace")
	log.Printf("Restart completed with '%s'", output)

	return err
}

func StartUnitByName(name string) error {
	conn, err := dbus.New()
	if err != nil {
		return err
	}

	_, err = conn.StartUnit(name, "replace")
	return err
}

func ExecuteScript(scriptPath string) (string, error) {
	props := []dbus.Property{
		dbus.PropDescription("Unit generated and executed by coreos-cloudinit on behalf of user"),
		dbus.PropExecStart([]string{"/bin/bash", scriptPath}, false),
	}

	base := path.Base(scriptPath)
	name := fmt.Sprintf("coreos-cloudinit-%s.service", base)

	log.Printf("Creating transient systemd unit '%s'", name)

	conn, err := dbus.New()
	if err != nil {
		return "", err
	}

	_, err = conn.StartTransientUnit(name, "replace", props...)
	return name, err
}

func SetHostname(hostname string) error {
	return exec.Command("hostnamectl", "set-hostname", hostname).Run()
}

func Hostname() (string, error) {
	return os.Hostname()
}

func MachineID(root string) string {
	contents, _ := ioutil.ReadFile(path.Join(root, "etc", "machine-id"))
	id := strings.TrimSpace(string(contents))

	if id == fakeMachineID {
		id = ""
	}

	return id
}
