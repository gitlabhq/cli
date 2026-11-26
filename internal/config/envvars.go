package config

import (
	"fmt"
	"slices"
	"strings"
)

// EnvVarGroup splits the reference into sections, since a single table of
// every variable is hard to scan.
type EnvVarGroup string

const (
	GroupAccess    EnvVarGroup = "GitLab access variables"
	GroupBehaviour EnvVarGroup = "`glab` configuration variables"
	GroupOther     EnvVarGroup = "Other variables"
)

var EnvVarGroupOrder = []EnvVarGroup{GroupAccess, GroupBehaviour, GroupOther}

// EnvVarDoc is one entry of the environment variable reference. Description
// follows the same convention as KeyDef.Description: plain prose with
// 'single-quoted' literals, left for the renderer to mark up.
type EnvVarDoc struct {
	Name        string
	ConfigKey   string
	Default     string
	Description string
	Group       EnvVarGroup
}

// nonSchemaEnvVars are read directly with os.Getenv rather than resolved
// through KeySchema, so they have to be listed by hand.
var nonSchemaEnvVars = []EnvVarDoc{
	{Name: "FORCE_HYPERLINKS", ConfigKey: "display_hyperlinks", Description: "Set to 1 to force terminal hyperlinks when not writing to a TTY. A falsy value falls through to 'display_hyperlinks'.", Group: GroupBehaviour},
	{Name: "GITLAB_GROUP", Description: "Default group for supported commands, including 'glab mr list', 'glab issue list', 'glab variable list', 'glab deploy-token get', and 'glab deploy-token list'. An explicit '--group' or '--repo' takes precedence.", Group: GroupAccess},
	{Name: "GITLAB_ORGANIZATION_ID", Description: "Numeric ID of the GitLab organization that glab's requests belong to. Sent as an HTTP header so a GitLab Cells deployment can route the request.", Group: GroupAccess},
	{Name: "GITLAB_HEAD_REPO", Description: "Source repository for 'glab mr create'. Used only when '--head' is not given.", Group: GroupAccess},
	{Name: "GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY", Description: "Set to true or 1 to upload release assets to the generic package registry of the project. The '--use-package-registry' flag takes precedence.", Group: GroupBehaviour},
	{Name: "GITLAB_REPO", Description: "Default repository for commands that accept '--repo'. Used only when '--repo' is not given.", Group: GroupAccess},
	{Name: "GLAB_CONFIG_DIR", Default: "~/.config/glab-cli", Description: "Directory holding the global configuration file. Takes priority over the XDG locations.", Group: GroupBehaviour},
	{Name: "GLAB_DEBUG_HTTP", Default: "false", Description: "Set to true to output HTTP transport information (request and response).", Group: GroupBehaviour},
	{Name: "GLAB_ENABLE_CI_AUTOLOGIN", Default: "false", Description: "Set to true to enable auto-login in GitLab CI. Together with 'GITLAB_CI=true', glab signs in using predefined CI/CD variables such as 'CI_SERVER_FQDN' and 'CI_JOB_TOKEN', and ignores host variables such as 'GITLAB_HOST'.", Group: GroupAccess},
	{Name: "GLAB_FORCE_HYPERLINKS", ConfigKey: "display_hyperlinks", Description: "Set to true to force terminal hyperlinks when not writing to a TTY.", Group: GroupBehaviour},
	{Name: "NO_COLOR", Description: "Set to any value to avoid printing ANSI escape sequences for color output.", Group: GroupBehaviour},
}

// groupFor splits per-host keys, which describe reaching an instance, from
// global keys, which describe how glab behaves. The exceptions are listed.
func groupFor(kd KeyDef) EnvVarGroup {
	switch kd.Name {
	case "host":
		return GroupAccess
	case "remote_alias":
		return GroupOther
	}
	if kd.Scope == ScopePerHost {
		return GroupAccess
	}
	return GroupBehaviour
}

// undocumentedEnvVarKeys resolve from the environment like any other key, but
// listing them would advertise a way to set them that nobody can act on: a job
// token only works inside CI, where it arrives as CI_JOB_TOKEN via auto-login.
var undocumentedEnvVarKeys = map[string]struct{}{
	"job_token": {},
}

// EnvVarsForKey returns the environment variables that set kd, most preferred
// first. TypeList keys are read straight out of the YAML rather than through
// Config.Get, so no environment variable reaches them.
func EnvVarsForKey(kd KeyDef) []string {
	if kd.Type == TypeList {
		return nil
	}
	if _, ok := undocumentedEnvVarKeys[kd.Name]; ok {
		return nil
	}
	return schemaEnvVars(kd.Name)
}

// EnvVars returns every environment variable glab reads, one entry per name so
// that a reader searching for the name they already have set finds it. Entries
// sort by the preferred name for the key, keeping alternatives directly beneath
// the entry that carries the real description.
func EnvVars() []EnvVarDoc {
	type ordered struct {
		doc     EnvVarDoc
		primary string
		rank    int
	}

	var rows []ordered
	for _, ev := range nonSchemaEnvVars {
		rows = append(rows, ordered{doc: ev, primary: ev.Name})
	}
	for _, kd := range KeySchema {
		if !kd.UserSettable || kd.HelpHidden {
			continue
		}
		envVars := EnvVarsForKey(kd)
		for i, name := range envVars {
			desc := kd.Description
			if i > 0 {
				desc = fmt.Sprintf("Alternative name for '%s', checked after it.", envVars[0])
			}
			rows = append(rows, ordered{
				doc: EnvVarDoc{
					Name:        name,
					ConfigKey:   kd.Name,
					Default:     kd.Default,
					Description: desc,
					Group:       groupFor(kd),
				},
				primary: envVars[0],
				rank:    i,
			})
		}
	}

	slices.SortFunc(rows, func(a, b ordered) int {
		if c := strings.Compare(a.primary, b.primary); c != 0 {
			return c
		}
		return a.rank - b.rank
	})

	out := make([]EnvVarDoc, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.doc)
	}
	return out
}

// EnvVarSection is one rendered group of the reference.
type EnvVarSection struct {
	Group EnvVarGroup
	Vars  []EnvVarDoc
}

// EnvVarsByGroup returns EnvVars split into EnvVarGroupOrder, skipping any
// group that ends up empty.
func EnvVarsByGroup() []EnvVarSection {
	var out []EnvVarSection
	all := EnvVars()
	for _, group := range EnvVarGroupOrder {
		var vars []EnvVarDoc
		for _, ev := range all {
			if ev.Group == group {
				vars = append(vars, ev)
			}
		}
		if len(vars) > 0 {
			out = append(out, EnvVarSection{Group: group, Vars: vars})
		}
	}
	return out
}
