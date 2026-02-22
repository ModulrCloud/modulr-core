package dashboard

import "embed"

//go:embed dashboard.html
var dashboardFS embed.FS

func ReadDashboardHTML() ([]byte, error) {
	return dashboardFS.ReadFile("dashboard.html")
}

