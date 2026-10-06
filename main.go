package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/lukaulcar/sper/pkg/config"
	"github.com/lukaulcar/sper/pkg/tunnel"
)

type extras []string

func (e *extras) String() string { return strings.Join(*e, ",") }
func (e *extras) Set(v string) error {
	*e = append(*e, v)
	return nil
}

func main() {
	// ssh calls us back for password auth
	if os.Getenv("SPER_IS_ASKPASS") == "1" {
		fmt.Println(os.Getenv("SPER_ASKPASS_PASS"))
		return
	}

	var cfgPath, ident, sport string
	var health, verbose, listFlag bool
	var opts extras

	flag.StringVar(&cfgPath, "c", "", "config file")
	flag.StringVar(&cfgPath, "config", "", "config file")
	flag.StringVar(&ident, "i", "", "key file")
	flag.StringVar(&sport, "p", "", "ssh port")
	flag.BoolVar(&health, "healthcheck", true, "probe local ports")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.BoolVar(&listFlag, "list", false, "list profiles")
	flag.Var(&opts, "o", "extra ssh -o option")
	flag.Usage = usage
	flag.Parse()

	log.SetFlags(0)

	sshBin, err := tunnel.FindSSHBinary()
	if err != nil {
		log.Fatal(err)
	}

	if listFlag {
		profs, path, err := config.LoadConfigFile(cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		if len(profs) == 0 {
			fmt.Println("no config / profiles found")
			return
		}
		showProfiles(profs, path)
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		usage()
		if profs, path, _ := config.LoadConfigFile(cfgPath); len(profs) > 0 {
			fmt.Println()
			showProfiles(profs, path)
		}
		os.Exit(1)
	}

	var tc tunnel.Config
	if len(args) == 1 {
		tc = fromProfile(args[0], cfgPath, ident, sport, verbose, opts, sshBin)
	} else {
		tc = fromArgs(args, ident, sport, health, verbose, opts, sshBin)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tunnel.Run(ctx, tc)
}

func fromProfile(name, cfgPath, ident, sport string, verbose bool, opts []string, sshBin string) tunnel.Config {
	if name == "list" || name == "profiles" {
		profs, path, err := config.LoadConfigFile(cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		if len(profs) == 0 {
			fmt.Println("no config / profiles found")
			os.Exit(0)
		}
		showProfiles(profs, path)
		os.Exit(0)
	}

	profs, path, err := config.LoadConfigFile(cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if len(profs) == 0 {
		log.Fatal("no config file found, make a sper.yaml or pass -c <path>")
	}

	p, ok := profs[name]
	if !ok {
		names := []string{}
		for k := range profs {
			names = append(names, k)
		}
		log.Fatalf("unknown profile %q (have: %s) in %s", name, strings.Join(names, ", "), path)
	}

	tc, err := p.ToConfig(name, sshBin)
	if err != nil {
		log.Fatal(err)
	}

	if cfgPath != "" {
		log.Printf("using %s\n", path)
	} else {
		log.Printf("loaded %s from %s\n", name, path)
	}

	if ident != "" {
		tc.IdentityFile = tunnel.ExpandPath(ident)
	}
	if sport != "" {
		tc.ServerPort = sport
	}
	if verbose {
		tc.Verbose = true
	}
	tc.ExtraOptions = append(tc.ExtraOptions, opts...)

	return tc
}

func fromArgs(args []string, ident, sport string, health, verbose bool, opts []string, sshBin string) tunnel.Config {
	server := args[len(args)-1]

	fwds := []tunnel.PortForward{}
	for _, raw := range args[:len(args)-1] {
		f, err := tunnel.ParsePortSpec(raw)
		if err != nil {
			log.Fatalf("bad port %q: %v", raw, err)
		}
		fwds = append(fwds, f)
	}

	idPath := ""
	if ident != "" {
		idPath = tunnel.ExpandPath(ident)
		if _, err := os.Stat(idPath); err != nil {
			log.Fatalf("key not found %s: %v", idPath, err)
		}
	}

	return tunnel.Config{
		SSHBinary:    sshBin,
		TargetServer: server,
		IdentityFile: idPath,
		ServerPort:   sport,
		Forwards:     fwds,
		CheckHealth:  health,
		Verbose:      verbose,
		ExtraOptions: opts,
	}
}

func showProfiles(profs map[string]config.Profile, path string) {
	fmt.Printf("profiles (%s):\n", path)
	for name, p := range profs {
		ports := []string{}
		for _, pt := range p.Ports {
			ports = append(ports, fmt.Sprintf("%v", pt))
		}
		fmt.Printf("  %-15s -> %-15s [%s]\n", name, p.Server, strings.Join(ports, ", "))
	}
	fmt.Println("\nrun: sper <profile>")
}

func usage() {
	prog := filepath.Base(os.Args[0])
	if runtime.GOOS == "windows" {
		prog = strings.TrimSuffix(prog, ".exe")
	}

	fmt.Printf("sper - keep ssh tunnels up\n\n")
	fmt.Printf("usage:\n")
	fmt.Printf("  %s [flags] <profile>\n", prog)
	fmt.Printf("  %s [flags] <ports...> <server>\n\n", prog)
	fmt.Printf("examples:\n")
	fmt.Printf("  %s dev-db\n", prog)
	fmt.Printf("  %s 8080 3000 my-server\n", prog)
	fmt.Printf("  %s 5432:db.internal:5432 user@192.168.1.50\n\n", prog)
	fmt.Println("port can be 8080, 3000:8000, 5432:db:5432, 0.0.0.0:8080:host:80")
	fmt.Println()
	fmt.Println("flags:")
	flag.PrintDefaults()
	fmt.Println()
	fmt.Println("config is looked up in -c path, exe dir, ./sper.yaml, last -c used, ~/.config/sper/")
}
