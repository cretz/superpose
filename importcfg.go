package superpose

import (
	"fmt"
	"os"
	"strings"
)

type importCfg struct {
	s     *Superpose
	lines []string
}

func (s *Superpose) loadImportCfg(file string) (*importCfg, error) {
	importCfgBytes, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return &importCfg{s: s, lines: strings.Split(strings.TrimSpace(string(importCfgBytes)), "\n")}, nil
}

func (i *importCfg) removePkgFile(pkgPath string) bool {
	for idx, line := range i.lines {
		if strings.HasPrefix(line, "packagefile "+pkgPath+"=") {
			// We are ok w/ the wasted space here, an add usually comes right after
			i.lines = append(i.lines[:idx], i.lines[idx+1:]...)
			return true
		}
	}
	return false
}

func (i *importCfg) addPkgFile(pkgPath string, pkgFile string) {
	// We only add if not already there
	for _, line := range i.lines {
		if strings.HasPrefix(line, "packagefile "+pkgPath+"=") {
			return
		}
	}
	i.lines = append(i.lines, fmt.Sprintf("packagefile %v=%v", pkgPath, pkgFile))
}

// Only done if not already present
func (i *importCfg) includePkg(pkgPath string) error {
	// Check that it's not already present
	for _, line := range i.lines {
		if strings.HasPrefix(line, "packagefile "+pkgPath+"=") {
			return nil
		}
	}
	// Since it's not in there, load the pkg file and add it
	pkgFile, err := i.s.pkgFile(pkgPath)
	if err != nil {
		return err
	}
	i.lines = append(i.lines, fmt.Sprintf("packagefile %v=%v", pkgPath, pkgFile))
	return nil
}

// If replace is true, removes orig before adding new
func (i *importCfg) updateDimPkgRefs(d dimPkgRefs, replace bool) error {
	// We don't care if import cfg is deterministic, so we can loop here
	for dim, origPkgs := range d {
		for origPkg := range origPkgs {
			pkgFile, err := i.s.dimDepPkgFile(origPkg, dim)
			if err != nil {
				return err
			}
			if replace {
				i.removePkgFile(origPkg)
			}
			i.addPkgFile(i.s.DimensionPackagePath(origPkg, dim), pkgFile)
		}
	}
	return nil
}

func (i *importCfg) buildContent() string {
	// We add a newline at the end like Go does
	return strings.Join(i.lines, "\n") + "\n"
}

func (i *importCfg) writeFile(file string) error {
	content := i.buildContent()
	i.s.Debugf("Writing importcfg to %v with content:\n%v", file, content)
	return os.WriteFile(file, []byte(content), 0666)
}

func (i *importCfg) writeTempFile() (string, error) {
	tmpDir, err := i.s.UseTempDir()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(tmpDir, "importcfg-")
	if err != nil {
		return "", err
	}
	defer f.Close()
	content := i.buildContent()
	i.s.Debugf("Writing importcfg to %v with content:\n%v", f.Name(), content)
	_, err = f.Write([]byte(content))
	if err != nil {
		return "", err
	}
	return f.Name(), nil
}

// Key is dimension, key of sub map is orig package path
type dimPkgRefs map[string]map[string]struct{}

func (d dimPkgRefs) addRef(origPkgPath string, dim string) {
	pkgMap := d[dim]
	if pkgMap == nil {
		pkgMap = map[string]struct{}{}
		d[dim] = pkgMap
	}
	pkgMap[origPkgPath] = struct{}{}
}
