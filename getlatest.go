// Install:
//
//	go get github.com/tomclegg/getlatest
//
// systemd:
//
//	install $(go env GOPATH)/bin/getlatest /usr/bin/
//	getlatest -install-service
//
// Standalone:
//
//	getlatest &
//
// Config:
//
//	# /etc/getlatest.yaml
//	/tmp/example.html:
//	  URL: "https://host.example/source/example?t={{.time.Format \"2016-01-02T15:04.05\"}}.html"
//	  NotBefore: 6:00
//	  NotAfter: 13:00
//	  Weekdays: mon tue wed thu fri
//	  MinimumSize: 14000000
//	  TTL: 12h
//
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghodss/yaml"
)

type getter struct {
	URL         string
	Output      string
	NotBefore   string
	NotAfter    string
	Weekdays    string
	MinimumSize int64
	TTL         string

	urlt        *template.Template
	ttl         time.Duration
	lastSuccess time.Time
}

const defaultConfigPath = "/etc/getlatest.yaml"

func main() {
	log.SetFlags(0)

	installService := flag.Bool("install-service", false, "install systemd service")
	configPath := flag.String("config", defaultConfigPath, "configuration `file`")
	flag.Parse()
	if *installService {
		err := ioutil.WriteFile("/lib/systemd/system/getlatest.service", systemdUnitFile, 0666)
		if err != nil {
			log.Fatal(err)
		}
		for _, cmd := range []*exec.Cmd{
			exec.Command("systemctl", "daemon-reload"),
			exec.Command("systemctl", "enable", "--now", "getlatest.service"),
		} {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err = cmd.Run()
			if err != nil {
				log.Fatalf("%q: %s", cmd.Args, err)
			}
		}
		return
	}

	var getters map[string]*getter
	buf, err := ioutil.ReadFile(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	err = yaml.Unmarshal(buf, &getters)
	if err != nil {
		log.Fatal(err)
	}
	for output, g := range getters {
		g.Output = output
		err = g.setup()
		if err != nil {
			log.Fatal(err)
		}
	}
	for _, g := range getters {
		go g.run()
	}
	<-(chan bool)(nil)
}

func (g *getter) url() (string, error) {
	var buf bytes.Buffer
	err := g.urlt.Execute(&buf, map[string]interface{}{"time": time.Now()})
	return buf.String(), err
}

func (g *getter) setup() error {
	if urlt, err := template.New("url").Parse(g.URL); err != nil {
		return err
	} else {
		g.urlt = urlt
	}
	if urlstr, err := g.url(); err != nil {
		return err
	} else if url, err := url.Parse(urlstr); err != nil {
		return err
	} else if url.Scheme == "" {
		return fmt.Errorf("%q: cannot use URL %q with no protocol scheme", g.Output, g.URL)
	}

	if fi, err := os.Stat(g.Output); err == nil {
		g.lastSuccess = fi.ModTime()
	}
	if t, err := time.Parse("15:04", g.NotBefore); err != nil && g.NotBefore != "" {
		return fmt.Errorf("%q: error parsing NotBefore value %q: %s", g.Output, g.NotBefore, err)
	} else if err == nil {
		g.NotBefore = t.Format("15:04")
	}
	if t, err := time.Parse("15:04", g.NotAfter); err != nil && g.NotAfter != "" {
		return fmt.Errorf("%q: error parsing NotAfter value %q: %s", g.Output, g.NotAfter, err)
	} else if err == nil {
		g.NotAfter = t.Format("15:04")
	}
	if d, err := time.ParseDuration(g.TTL); g.TTL == "" {
		g.ttl = time.Hour
		log.Printf("%q: using default TTL %s", g.Output, g.ttl)
	} else if err != nil {
		return fmt.Errorf("%q: error parsing TTL value %q: %s", g.Output, g.TTL, err)
	} else {
		g.ttl = d
	}
	if g.Weekdays = strings.TrimSpace(g.Weekdays); g.Weekdays != "" {
		g.Weekdays = " " + strings.ToLower(g.Weekdays)
	}
	return nil
}

func (g *getter) run() {
	g.download()
	for range time.NewTicker(time.Minute).C {
		g.download()
	}
}

func (g *getter) should(t time.Time) bool {
	if t.Sub(g.lastSuccess) < g.ttl {
		return false
	}
	now := t.Format("15:04")
	if g.NotBefore != "" && strings.Compare(now, g.NotBefore) < 0 {
		return false
	}
	if g.NotAfter != "" && strings.Compare(now, g.NotAfter) > 0 {
		return false
	}
	if g.Weekdays != "" && !strings.Contains(g.Weekdays, " "+strings.ToLower(t.Format("Mon"))) {
		return false
	}
	return true
}

func (g *getter) download() {
	if !g.should(time.Now()) {
		return
	}
	url, err := g.url()
	if err != nil {
		log.Printf("%q: error getting url: %s", g.Output, err)
		return
	}
	log.Printf("%q: downloading %q", g.Output, url)
	outdir, outfile := filepath.Split(g.Output)
	f, err := ioutil.TempFile(outdir, "."+outfile+".")
	if err != nil {
		log.Printf("%q: error creating tempfile: %s", g.Output, err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("%q: %q: %s", g.Output, url, err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("%q: %q: non-OK response: %d %q", g.Output, url, resp.StatusCode, resp.Status)
		return
	}
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		log.Printf("%q: downloading %q to tempfile: %s", g.Output, url, err)
		return
	}
	if n < g.MinimumSize {
		log.Printf("%q: response body too small: %d bytes < MinimumSize %d", g.Output, n, g.MinimumSize)
		return
	}
	err = f.Close()
	if err != nil {
		log.Printf("%q: writing tempfile: %s", g.Output, err)
		return
	}
	err = os.Rename(f.Name(), g.Output)
	if err != nil {
		log.Printf("%q: renaming tempfile: %s", g.Output, err)
		return
	}
	g.lastSuccess = time.Now()
	log.Printf("%q: success, wrote %d bytes", g.Output, n)
}

var systemdUnitFile = []byte(`
[Unit]
Description=getlatest
After=network.target
StartLimitIntervalSec=0
ConditionPathExists=` + defaultConfigPath + `

[Service]
Type=simple
ExecStart=/usr/bin/env getlatest
RestartSec=60
Restart=always
SyslogIdentifier=getlatest

[Install]
WantedBy=multi-user.target
`)