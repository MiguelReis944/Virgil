package app

import "os/exec"

func openBrowser(panelURL string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", panelURL)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
