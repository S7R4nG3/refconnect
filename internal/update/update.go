// Package update checks GitHub for newer RefConnect releases and installs
// them by running the project's setup script in the install directory.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	latestReleaseAPI = "https://api.github.com/repos/S7R4nG3/refconnect/releases/latest"
	setupScriptBase  = "https://raw.githubusercontent.com/S7R4nG3/refconnect/main/configs/"

	// oldSuffix is appended to the running executable while the setup script
	// replaces it. Windows cannot overwrite a running .exe, and on macOS/Linux
	// writing over a mapped binary can crash the running process.
	oldSuffix = ".old"
)

// CheckLatest returns the latest release tag on GitHub and whether it is
// newer than current (e.g. "v0.12.0").
func CheckLatest(ctx context.Context, current string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseAPI, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("release check: HTTP %d", resp.StatusCode)
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", false, fmt.Errorf("release check: %w", err)
	}
	if rel.TagName == "" {
		return "", false, fmt.Errorf("release check: no tag_name in response")
	}
	return rel.TagName, isNewer(rel.TagName, current), nil
}

// isNewer reports whether version a is strictly greater than b. Versions are
// "vMAJOR.MINOR.PATCH"; any pre-release suffix is ignored.
func isNewer(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, part := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(part)
	}
	return out
}

// Install downloads the platform's setup script and runs it in the directory
// RefConnect is installed in, replacing the current version with the latest
// release. It returns the path to relaunch (the .app bundle on macOS, the
// executable elsewhere). The script's output is passed to logf.
func Install(ctx context.Context, logf func(string, ...any)) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	installDir, relaunch := installLocation(exe)

	scriptName, interpreter := "setup.sh", []string{"sh"}
	if runtime.GOOS == "windows" {
		scriptName = "setup.ps1"
		interpreter = []string{"powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}
	}
	script, err := downloadScript(ctx, scriptName)
	if err != nil {
		return "", err
	}
	defer os.Remove(script)

	// Move the running executable out of the way so the script can write the
	// new one in its place. Restored if the script fails.
	old := exe + oldSuffix
	os.Remove(old) //nolint:errcheck
	if err := os.Rename(exe, old); err != nil {
		return "", fmt.Errorf("moving current executable aside: %w", err)
	}

	cmd := exec.CommandContext(ctx, interpreter[0], append(interpreter[1:], script)...)
	cmd.Dir = installDir
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			logf("update: %s", line)
		}
	}
	if err == nil {
		if _, statErr := os.Stat(exe); statErr != nil {
			err = fmt.Errorf("setup script did not produce %s", filepath.Base(exe))
		}
	}
	if err != nil {
		os.Remove(exe)      //nolint:errcheck
		os.Rename(old, exe) //nolint:errcheck
		return "", fmt.Errorf("running %s: %w", scriptName, err)
	}
	return relaunch, nil
}

// CleanupOld removes the executable left behind by a previous update.
func CleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = filepath.EvalSymlinks(exe); err == nil {
		os.Remove(exe + oldSuffix) //nolint:errcheck
	}
}

// Relaunch starts the newly installed version as a separate process.
func Relaunch(path string) error {
	if runtime.GOOS == "darwin" && strings.HasSuffix(path, ".app") {
		// -n forces a new instance even though this one is still running.
		return exec.Command("open", "-n", path).Start()
	}
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	return cmd.Start()
}

// installLocation returns the directory the setup script should run in and
// the path to relaunch afterwards. The setup script extracts into its
// working directory, so on macOS that is the folder containing
// RefConnect.app rather than the bundle's Contents/MacOS directory.
func installLocation(exe string) (dir, relaunch string) {
	if runtime.GOOS == "darwin" {
		macOS := filepath.Dir(exe)
		contents := filepath.Dir(macOS)
		bundle := filepath.Dir(contents)
		if filepath.Base(macOS) == "MacOS" && filepath.Base(contents) == "Contents" &&
			strings.HasSuffix(bundle, ".app") {
			return filepath.Dir(bundle), bundle
		}
	}
	return filepath.Dir(exe), exe
}

// downloadScript fetches the named setup script to a temporary file.
func downloadScript(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, setupScriptBase+name, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: HTTP %d", name, resp.StatusCode)
	}

	f, err := os.CreateTemp("", "refconnect-*-"+name)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
