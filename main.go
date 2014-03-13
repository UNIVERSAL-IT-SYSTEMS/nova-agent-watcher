package main

import (
	"code.google.com/p/go.exp/fsnotify"
	"flag"
	"fmt"
	"github.com/coreos/coreos-cloudinit/cloudinit"
	"github.com/coreos/go-systemd/dbus"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

var fileHandlers = map[string]func(string) (*cloudinit.CloudConfig, error){
	"/etc/conf.d/net": handleNet,
}

func main() {
	var watch_dir = flag.String("watch-dir", ".", "Path to watch")
	flag.Parse()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}

	done := make(chan bool)

	// Process events
	go func() {
		for {
			select {
			case ev := <-watcher.Event:
				log.Println("got event", ev)
				if !ev.IsCreate() {
					continue
				}
				err := runEvent(ev.Name, watch_dir)
				if err != nil {
					log.Println("error handling event:", err)
				}
			case err := <-watcher.Error:
				log.Println("error:", err)
				done <- true
			}
		}
	}()

	for k, _ := range fileHandlers {
		full_path := filepath.Join(*watch_dir, k)
		dir_path := filepath.Dir(full_path)
		err = watcher.Watch(dir_path)
		if err != nil {
			log.Println("warn: error setting up watcher (dir doesn't exist?):", err)
		}
		err = runEvent(full_path, watch_dir)
		if err != nil {
			log.Println("warn: initalizing event failed:", err)
		}
	}

	<-done
	watcher.Close()
}

func runEvent(full_path string, watch_dir *string) error {
	if _, err := os.Stat(full_path); err != nil {
		return err
	}
	file_name, err := filepath.Rel(*watch_dir, full_path)
	if err != nil {
		log.Println("error getting relative path for:", full_path)
		return err
	}
	func_key := filepath.Join("/", file_name)
	if err != nil {
		log.Println("error getting joining path for:", full_path)
		return err
	}
	config, err := fileHandlers[func_key](full_path)
	if err != nil {
		log.Println("error in handler", err)
		return err
	}
	err = runConfig(config)
	return err
}

func runConfig(config *cloudinit.CloudConfig) error {
	f, err := ioutil.TempFile("", "rackspace-cloudinit-")
	if err != nil {
		return err
	}
	log.Println("writing to:", f.Name())
	_, err = f.WriteString(config.String())
	if err != nil {
		return err
	}
	// systemd-run coreos-cloudinit --file f.Name()
	props := []dbus.Property{
		dbus.PropDescription("Unit generated and executed by coreos-cloudinit on behalf of user"),
		dbus.PropExecStart([]string{"/usr/bin/coreos-cloudinit", "--from-file", f.Name()}, false),
	}

	tmp_file := filepath.Base(f.Name())
	name := fmt.Sprintf("%s.service", tmp_file)
	log.Printf("Creating transient systemd unit '%s'", name)

	conn, err := dbus.New()
	if err != nil {
		return err
	}
	_, err = conn.StartTransientUnit(name, "replace", props...)
	return err
}

func handleNet(file_name string) (*cloudinit.CloudConfig, error) {
	contents, err := ioutil.ReadFile(file_name)
	if err != nil {
		log.Println("error: could not read file", err)
		return nil, err
	}
	network_str := string(contents)

	re := regexp.MustCompile("eth[\\d]+")
	eths := re.FindAllString(network_str, -1)

	config := cloudinit.CloudConfig{}

	configured_eths := map[string]bool{}
	for _, eth := range eths {
		// hack to prevent multiple regex matches from creating multiple files
		if configured_eths[eth] {
			continue
		}
		cmd := exec.Command("./scripts/gentoo-to-networkd", eth, file_name)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Println("error: not good output", err)
			return nil, err
		}
		unit := fmt.Sprintf("50-%s.network", eth)
		u := cloudinit.Unit{
			Name:    unit,
			Content: string(out),
		}
		config.Coreos.Units = append(config.Coreos.Units, u)
		configured_eths[eth] = true
	}
	return &config, nil
}
