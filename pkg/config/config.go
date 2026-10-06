package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lukaulcar/sper/pkg/tunnel"
	"gopkg.in/yaml.v3"
)

type Profile struct {
	Server      string   `yaml:"server"`
	Ports       []any    `yaml:"ports"`
	Identity    string   `yaml:"identity"`
	Port        any      `yaml:"port"`
	Password    string   `yaml:"password"`
	Healthcheck *bool    `yaml:"healthcheck"`
	Verbose     bool     `yaml:"verbose"`
	Options     []string `yaml:"options"`
}

type fileConf struct {
	Profiles map[string]Profile `yaml:"profiles"`
}

func (p Profile) ToConfig(name, sshBin string) (tunnel.Config, error) {
	server := strings.TrimSpace(p.Server)
	if server == "" {
		return tunnel.Config{}, fmt.Errorf("profile %q has no server set", name)
	}

	fwds, err := parsePorts(p.Ports)
	if err != nil {
		return tunnel.Config{}, fmt.Errorf("profile %q: %v", name, err)
	}

	ident := strings.TrimSpace(p.Identity)
	if ident != "" {
		ident = tunnel.ExpandPath(ident)
		if _, err := os.Stat(ident); err != nil {
			return tunnel.Config{}, fmt.Errorf("profile %q: key not found: %s", name, ident)
		}
	}

	sport := ""
	if p.Port != nil {
		sport = strings.TrimSpace(fmt.Sprintf("%v", p.Port))
		if sport == "0" || sport == "<nil>" {
			sport = ""
		}
	}

	health := true
	if p.Healthcheck != nil {
		health = *p.Healthcheck
	}

	return tunnel.Config{
		SSHBinary:    sshBin,
		TargetServer: server,
		IdentityFile: ident,
		ServerPort:   sport,
		Password:     p.Password,
		Forwards:     fwds,
		CheckHealth:  health,
		Verbose:      p.Verbose,
		ExtraOptions: p.Options,
	}, nil
}

// returns profiles + which file they came from
func LoadConfigFile(explicit string) (map[string]Profile, string, error) {
	path := findFile(explicit)
	if path == "" {
		if explicit != "" {
			return nil, "", fmt.Errorf("config not found: %s", explicit)
		}
		return nil, "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("cant read %s: %v", path, err)
	}

	var fc fileConf
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, path, fmt.Errorf("bad yaml in %s: %v", path, err)
	}
	if len(fc.Profiles) == 0 {
		return nil, path, fmt.Errorf("no profiles in %s", path)
	}

	return fc.Profiles, path, nil
}

func findFile(explicit string) string {
	if explicit != "" {
		p := tunnel.ExpandPath(explicit)
		if _, err := os.Stat(p); err == nil {
			saveLastPath(p)
			return p
		}
		return ""
	}

	cands := []string{}

	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		cands = append(cands, filepath.Join(filepath.Dir(exe), "sper.yaml"))
	}

	cands = append(cands, "sper.yaml")

	// last -c path I used
	if last := readLastPath(); last != "" {
		cands = append(cands, last)
	}

	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands,
			filepath.Join(home, ".config", "sper", "config.yaml"),
			filepath.Join(home, ".sper.yaml"),
		)
	}

	if runtime.GOOS == "windows" {
		if ad := os.Getenv("APPDATA"); ad != "" {
			cands = append(cands, filepath.Join(ad, "sper", "config.yaml"))
		}
	}

	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func parsePorts(raw []any) ([]tunnel.PortForward, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("no ports set")
	}
	out := []tunnel.PortForward{}
	for _, r := range raw {
		s := strings.TrimSpace(fmt.Sprintf("%v", r))
		if s == "" {
			continue
		}
		f, err := tunnel.ParsePortSpec(s)
		if err != nil {
			return nil, fmt.Errorf("bad port %q: %v", s, err)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ports set")
	}
	return out, nil
}

func lastPathFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "sper", "last_config_path.txt")
}

func saveLastPath(p string) {
	f := lastPathFile()
	if f == "" {
		return
	}
	os.MkdirAll(filepath.Dir(f), 0755)
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	os.WriteFile(f, []byte(strings.TrimSpace(p)), 0644)
}

func readLastPath() string {
	f := lastPathFile()
	if f == "" {
		return ""
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if _, err := os.Stat(s); err == nil {
		return s
	}
	return ""
}
