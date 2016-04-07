package main

// Import apiplexy plugins in a separate block (just because it looks nicer).
import (
	_ "github.com/12foo/apiplexy/auth/hmac"
	_ "github.com/12foo/apiplexy/backend/sql"
	_ "github.com/12foo/apiplexy/logging"
	_ "github.com/12foo/apiplexy/misc"
)

import (
	"fmt"
	"github.com/12foo/apiplexy"
	"github.com/codegangsta/cli"
	"github.com/skratchdot/open-golang/open"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"
)

var configPath string
var pidfile string

func listPlugins(c *cli.Context) {
	fmt.Printf("Available plugins:\n\n")
	avail := apiplexy.AvailablePlugins()
	pnames := make([]string, len(avail))
	i := 0
	for n, _ := range avail {
		pnames[i] = n
		i++
	}
	sort.Strings(pnames)

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)
	for _, name := range pnames {
		plugin := avail[name]
		fmt.Fprintf(w, "   %s\t %s\n", name, plugin.Description)
	}
	fmt.Fprintln(w)
	w.Flush()
}

func docPlugin(c *cli.Context) {
	if len(c.Args()) != 1 {
		fmt.Printf("Which documentation do you want to open? Try 'apiplexy plugin-doc <plugin-name>'.\n")
		os.Exit(1)
	}
	plugin, ok := apiplexy.AvailablePlugins()[c.Args()[0]]
	if !ok {
		fmt.Printf("Plugin '%s' not found. Try 'apiplexy plugins' to list available ones.\n", c.Args()[0])
		os.Exit(1)
	}
	fmt.Printf("Opening documentation for '%s' at: %s\n", plugin.Name, plugin.Link)
	open.Start(plugin.Link)
}

func generateConfig(c *cli.Context) {
	config, err := apiplexy.ExampleConfiguration(c.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't generate configuration: %s\n", err.Error())
		os.Exit(1)
	}
	yml, err := yaml.Marshal(&config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't generate configuration: %s\n", err.Error())
		os.Exit(1)
	}
	os.Stdout.Write(yml)
}

func fileOrPid(maybe string) (int, error) {
	pid, err := strconv.Atoi(maybe)
	if err == nil {
		return pid, nil
	}
	if _, err := os.Stat(maybe); err == nil {
		rawpid, err := ioutil.ReadFile(maybe)
		if err != nil {
			return 0, fmt.Errorf("pidfile exists, but couldn't read it: %s", maybe)
		}
		pid, err = strconv.Atoi(string(rawpid))
		if err != nil {
			return 0, fmt.Errorf("pidfile PID is not an integer: %s", maybe)
		}
		return pid, nil
	}
	return 0, nil
}

func initApiplex(configPath string) (http.Handler, apiplexy.ApiplexConfig, error) {
	yml, err := ioutil.ReadFile(os.ExpandEnv(configPath))
	config := apiplexy.ApiplexConfig{}
	if err != nil {
		return nil, config, fmt.Errorf("Couldn't read config file: %s\n", err.Error())
	}
	err = yaml.Unmarshal(yml, &config)
	if err != nil {
		return nil, config, fmt.Errorf("Couldn't parse configuration: %s\n", err.Error())
	}
	ap, err := apiplexy.New(config)
	if err != nil {
		return nil, config, fmt.Errorf("Couldn't initialize API proxy. %s\n", err.Error())
	}
	return ap, config, nil
}

func check(c *cli.Context) {
	if _, _, err := initApiplex(c.String("config")); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	fmt.Println("All OK.")
	os.Exit(0)
}

func start(c *cli.Context) {
	if pidfile != "" {
		pid, err := fileOrPid(pidfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			os.Exit(1)
		}
		if pid != 0 && pid != syscall.Getppid() {
			fmt.Fprintf(os.Stderr, "There is already a pidfile at '%s' that appears to belong to another apiplexy instance.")
			os.Exit(1)
		}
	}

	if configPath == "" {
		configPath = c.String("config")
	}

	ap, config, err := initApiplex(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}

	server := &http.Server{
		Addr:           "0.0.0.0:" + strconv.Itoa(config.Serve.Port),
		Handler:        ap,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 16,
	}

	fmt.Printf("Launching apiplexy on port %d.\n", config.Serve.Port)
	l, err := net.Listen("tcp", server.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't start server on %s: %s\n", server.Addr, err.Error())
		os.Exit(1)
	}

	// write pidfile and wait for restart signal
	if pidfile != "" {
		ioutil.WriteFile(pidfile, []byte(strconv.Itoa(syscall.Getpid())), 0600)
	}

	defer func() {
		// server shuts down, delete pidfile if it's still our PID in there
		if pidfile != "" {
			rp, _ := ioutil.ReadFile(pidfile)
			p, _ := strconv.Atoi(string(rp))
			if p == syscall.Getpid() {
				os.Remove(pidfile)
			}
		}
	}()

	server.Serve(l)
}

func main() {
	app := cli.NewApp()
	app.Name = "apiplexy"
	app.Usage = "Pluggable API gateway/proxy system."
	app.Commands = []cli.Command{
		{
			Name:    "plugins",
			Usage:   "Lists available apiplexy plugins",
			Aliases: []string{"ls"},
			Action:  listPlugins,
		},
		{
			Name:   "doc",
			Usage:  "Opens documentation webpage for a plugin",
			Action: docPlugin,
		},
		{
			Name:    "generate",
			Usage:   "Generates a configuration file with the specified plugins",
			Aliases: []string{"gen"},
			Action:  generateConfig,
		},
		{
			Name:   "start",
			Usage:  "Starts API proxy using specified config file",
			Action: start,
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "g",
					Usage: "Restart gracefully, i.e. replace a previous apiplexy",
				},
				cli.StringFlag{
					Name:  "config, c",
					Value: "apiplexy.yaml",
					Usage: "Location of configuration file",
				},
				cli.StringFlag{
					Name:  "pidfile, p",
					Value: "apiplexy.pid",
					Usage: "Location of PID file",
				},
			},
		},
		{
			Name:   "check",
			Usage:  "Check an apiplexy config",
			Action: check,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "config, c",
					Value: "apiplexy.yaml",
					Usage: "Location of configuration file",
				},
			},
		},
	}
	app.Run(os.Args)
}
