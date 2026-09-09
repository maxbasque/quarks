package web

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openInBrowser launches url in the OS default browser.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, bsd, …
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %q: %w", url, err)
	}
	return nil
}
