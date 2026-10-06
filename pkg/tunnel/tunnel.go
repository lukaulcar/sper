package tunnel

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lukaulcar/sper/pkg/platform"
)

const minBackoff = 1 * time.Second
const maxBackoff = 30 * time.Second
const stableAfter = 20 * time.Second

var gracePeriod = 3 * time.Second
var checkInterval = 5 * time.Second
var dialTimeout = 2 * time.Second
var maxFails = 2

type PortForward struct {
	BindHost   string
	LocalPort  string
	RemoteHost string
	RemotePort string
}

func (p PortForward) String() string {
	if p.BindHost != "" {
		return fmt.Sprintf("%s:%s -> %s:%s", p.BindHost, p.LocalPort, p.RemoteHost, p.RemotePort)
	}
	return fmt.Sprintf("%s -> %s:%s", p.LocalPort, p.RemoteHost, p.RemotePort)
}

type Config struct {
	SSHBinary    string
	TargetServer string
	IdentityFile string
	ServerPort   string
	Password     string
	Forwards     []PortForward
	CheckHealth  bool
	Verbose      bool
	ExtraOptions []string
}

func Run(ctx context.Context, cfg Config) {
	backoff := minBackoff

	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down")
			return
		default:
		}

		for _, f := range cfg.Forwards {
			if err := checkPortFree(f.BindHost, f.LocalPort); err != nil {
				log.Printf("warning: local port in use %s (%v)\n", f.String(), err)
			}
		}

		log.Printf("starting ssh to %s (%d forwards)\n", cfg.TargetServer, len(cfg.Forwards))

		sessCtx, cancel := context.WithCancel(ctx)

		var badReason string
		var mu sync.Mutex
		markBad := func(r string) {
			mu.Lock()
			if badReason == "" {
				badReason = r
			}
			mu.Unlock()
			cancel()
		}

		cmd := exec.CommandContext(sessCtx, cfg.SSHBinary, BuildSSHArgs(cfg)...)
		platform.SetSysProcAttr(cmd)

		cmd.Cancel = func() error {
			if runtime.GOOS == "windows" {
				return cmd.Process.Kill()
			}
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		cmd.WaitDelay = 3 * time.Second

		if cfg.Password != "" {
			if exe, err := os.Executable(); err == nil {
				log.Println("warning: using plaintext password from config, keys are better")
				cmd.Env = append(os.Environ(),
					"SSH_ASKPASS="+exe,
					"SSH_ASKPASS_REQUIRE=force",
					"DISPLAY=sper:0",
					"SPER_IS_ASKPASS=1",
					"SPER_ASKPASS_PASS="+cfg.Password,
				)
			}
		}

		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		start := time.Now()

		if err := cmd.Start(); err != nil {
			cancel()
			log.Printf("could not start ssh: %v\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = nextBackoff(backoff)
			continue
		}

		if cfg.CheckHealth {
			go watchHealth(sessCtx, cfg.Forwards, markBad)
		}

		err := cmd.Wait()
		cancel()

		if ctx.Err() != nil {
			log.Println("stopped")
			return
		}

		uptime := time.Since(start)
		mu.Lock()
		reason := badReason
		mu.Unlock()

		if reason != "" {
			log.Printf("tunnel lost (%s) after %s\n", reason, uptime.Round(time.Second))
		} else if err != nil {
			log.Printf("tunnel dropped (%v) after %s\n", err, uptime.Round(time.Second))
		} else {
			log.Printf("ssh closed after %s\n", uptime.Round(time.Second))
		}

		if uptime > stableAfter {
			backoff = minBackoff
		} else {
			backoff = nextBackoff(backoff)
		}

		log.Printf("reconnecting in %s...\n", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func watchHealth(ctx context.Context, fwds []PortForward, onBad func(string)) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(gracePeriod):
	}

	t := time.NewTicker(checkInterval)
	defer t.Stop()

	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var dead []string
			var lastErr error
			for _, f := range fwds {
				host := f.BindHost
				if host == "" || host == "0.0.0.0" || host == "*" {
					host = "127.0.0.1"
				}
				c, err := net.DialTimeout("tcp", net.JoinHostPort(host, f.LocalPort), dialTimeout)
				if err != nil {
					dead = append(dead, f.String())
					lastErr = err
				} else {
					c.Close()
				}
			}
			if len(dead) > 0 {
				fails++
				log.Printf("health check failed %d/%d (%s: %v)\n", fails, maxFails, strings.Join(dead, ", "), lastErr)
				if fails >= maxFails {
					onBad(fmt.Sprintf("%s dead after %d checks (%v)", strings.Join(dead, ", "), fails, lastErr))
					return
				}
			} else if fails > 0 {
				log.Println("health check ok again")
				fails = 0
			}
		}
	}
}

func checkPortFree(bindHost, port string) error {
	h := bindHost
	if h == "" || h == "*" || h == "0.0.0.0" {
		h = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(h, port))
	if err != nil {
		return fmt.Errorf("port %s on %s in use", port, h)
	}
	ln.Close()
	return nil
}

func BuildSSHArgs(cfg Config) []string {
	args := []string{}

	for _, f := range cfg.Forwards {
		if f.BindHost != "" {
			args = append(args, "-L", fmt.Sprintf("%s:%s:%s:%s", f.BindHost, f.LocalPort, f.RemoteHost, f.RemotePort))
		} else {
			args = append(args, "-L", fmt.Sprintf("%s:%s:%s", f.LocalPort, f.RemoteHost, f.RemotePort))
		}
	}

	// user -o first so they can override mine if they really want
	for _, o := range cfg.ExtraOptions {
		args = append(args, "-o", o)
	}

	args = append(args,
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "TCPKeepAlive=yes",
		"-N",
	)

	if cfg.IdentityFile != "" {
		args = append(args, "-i", cfg.IdentityFile)
	}
	if cfg.ServerPort != "" {
		args = append(args, "-p", cfg.ServerPort)
	}
	if cfg.Verbose {
		args = append(args, "-v")
	}

	return append(args, cfg.TargetServer)
}

// supports "8080", "3000:8000", "5432:db:5432", "0.0.0.0:8080:host:80"
func ParsePortSpec(spec string) (PortForward, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return PortForward{}, fmt.Errorf("empty port spec")
	}

	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 1:
		if !validPort(parts[0]) {
			return PortForward{}, fmt.Errorf("bad port %q", parts[0])
		}
		return PortForward{BindHost: "127.0.0.1", LocalPort: parts[0], RemoteHost: "localhost", RemotePort: parts[0]}, nil
	case 2:
		if !validPort(parts[0]) || !validPort(parts[1]) {
			return PortForward{}, fmt.Errorf("bad mapping %q, want local:remote", spec)
		}
		return PortForward{BindHost: "127.0.0.1", LocalPort: parts[0], RemoteHost: "localhost", RemotePort: parts[1]}, nil
	case 3:
		if !validPort(parts[0]) {
			return PortForward{}, fmt.Errorf("bad local port %q", parts[0])
		}
		if parts[1] == "" {
			return PortForward{}, fmt.Errorf("missing remote host in %q", spec)
		}
		if !validPort(parts[2]) {
			return PortForward{}, fmt.Errorf("bad remote port %q", parts[2])
		}
		return PortForward{BindHost: "127.0.0.1", LocalPort: parts[0], RemoteHost: parts[1], RemotePort: parts[2]}, nil
	case 4:
		if parts[0] == "" {
			return PortForward{}, fmt.Errorf("missing bind host in %q", spec)
		}
		if !validPort(parts[1]) {
			return PortForward{}, fmt.Errorf("bad local port %q", parts[1])
		}
		if parts[2] == "" {
			return PortForward{}, fmt.Errorf("missing remote host in %q", spec)
		}
		if !validPort(parts[3]) {
			return PortForward{}, fmt.Errorf("bad remote port %q", parts[3])
		}
		return PortForward{BindHost: parts[0], LocalPort: parts[1], RemoteHost: parts[2], RemotePort: parts[3]}, nil
	default:
		return PortForward{}, fmt.Errorf("bad spec %q", spec)
	}
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n > 0 && n <= 65535
}

func nextBackoff(cur time.Duration) time.Duration {
	if cur*2 < maxBackoff {
		return cur * 2
	}
	return maxBackoff
}

func FindSSHBinary() (string, error) {
	if p, err := exec.LookPath("ssh"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		fb := filepath.Join(root, "System32", "OpenSSH", "ssh.exe")
		if _, err := os.Stat(fb); err == nil {
			return fb, nil
		}
	}
	return "", fmt.Errorf("no ssh found in PATH")
}

func ExpandPath(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return filepath.Clean(os.ExpandEnv(p))
}
