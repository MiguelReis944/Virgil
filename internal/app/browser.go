package app

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// OpenBrowser opens a local panel URL in the user's default browser.
func OpenBrowser(panelURL string) error {
	if err := validatePanelURL(panelURL); err != nil {
		return err
	}
	return openBrowser(panelURL)
}

func validatePanelURL(panelURL string) error {
	u, err := url.Parse(panelURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return fmt.Errorf("invalid local panel URL")
	}
	host := net.ParseIP(u.Hostname())
	if host == nil || !host.IsLoopback() {
		return fmt.Errorf("panel URL must use a loopback host")
	}
	if u.Path != "/dashboard" && !strings.HasPrefix(u.Path, "/dashboard/") {
		return fmt.Errorf("panel URL must use /dashboard")
	}
	return nil
}
