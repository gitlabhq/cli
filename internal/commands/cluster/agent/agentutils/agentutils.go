package agentutils

import (
	"net/url"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

const (
	kasProxyProtocol = "https"
	kasProxyEndpoint = "k8s-proxy"
)

func DisplayAllAgents(io *iostreams.IOStreams, agents []*gitlab.Agent) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), c.Bold("Name"), c.Bold(c.Gray("Created At")))
	for _, r := range agents {
		table.AddRow(r.ID, r.Name, c.Gray(utils.TimeToPrettyTimeAgo(*r.CreatedAt)))
	}
	return table.Render()
}

func GetKasK8SProxyURL(m *gitlab.Metadata) (string, error) {
	switch {
	case m.KAS.ExternalK8SProxyURL != "":
		return m.KAS.ExternalK8SProxyURL, nil
	default:
		// NOTE: this fallback is only here because m.KAS.ExternalK8SProxyURL was recently introduced with 17.6.
		u, err := url.Parse(m.KAS.ExternalURL)
		if err != nil {
			return "", err
		}
		ku := *u.JoinPath(kasProxyEndpoint)
		ku.Scheme = kasProxyProtocol
		return ku.String(), nil
	}
}
