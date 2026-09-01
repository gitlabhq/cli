package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// KAS accepts only the "pat:" authorization scheme this command builds below.
var errUnsupportedToken = errors.New("cluster graph command supports authentication with only personal and project access tokens; requires at least the Developer role")

type options struct {
	io                    *iostreams.IOStreams
	apiClient             func(repoHost string) (*api.Client, error)
	baseRepo              func() (glrepo.Interface, error)
	listenNet, listenAddr string
	agentID               int64
	nsNames               []string
	nsLabels              string
	nsSelector            string
	nsCEL                 string
	resources             []string
	rootsCEL              []string
	readQueryFromStdIn    bool
	groupCore             bool
	groupBatch            bool
	groupApps             bool
	groupRBAC             bool
	groupClusterRBAC      bool
	groupCRD              bool
	logWatchRequest       bool
	ignoreArcDirection    bool
}

func NewCmdGraph(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,

		listenNet:  "tcp",
		listenAddr: "localhost:0",
	}
	graphCmd := &cobra.Command{
		Use:   "graph [flags]",
		Short: `Query the Kubernetes object graph using the GitLab Agent for Kubernetes. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			This command starts a web server that shows a live view of the Kubernetes object graph in a browser.
			It uses the GitLab Agent for Kubernetes running in the cluster.
			It requires:

			- Version 18.1 or later of GitLab and the GitLab Agent.
			- At least the Developer role in the agent project.
			- This command requires a personal access token or project access token
			  for authentication. The token must have the %[1]sread_api%[1]s and %[1]sk8s_proxy%[1]s scopes.

			Leave feedback in [issue 7900](https://gitlab.com/gitlab-org/cli/-/issues/7900).

			### Resource filtering

			To filter resources, namespaces, and select root objects, use
			[Common Expression Language (CEL)](https://cel.dev/).

			%[1]sobject_selector_expression%[1]s: Filters objects. The expression must return a boolean. These variables are available:

			- %[1]sobj%[1]s: The Kubernetes object being evaluated.
			- %[1]sgroup%[1]s: The group of the object.
			- %[1]sversion%[1]s: The version of the object.
			- %[1]sresource%[1]s: The resource name of the object, like %[1]spods%[1]s for the %[1]sPod%[1]s kind.
			- %[1]snamespace%[1]s: The namespace of the object.
			- %[1]sname%[1]s: The name of the object.
			- %[1]slabels%[1]s: The labels of the object.
			- %[1]sannotations%[1]s: The annotations of the object.

			%[1]sresource_selector_expression%[1]s: Filters Kubernetes discovery information to include or exclude resources
			from the watch request. The expression must return a boolean. These variables are available:

			- %[1]sgroup%[1]s: The group of the object.
			- %[1]sversion%[1]s: The version of the object.
			- %[1]sresource%[1]s: The resource name of the object, like %[1]spods%[1]s for the %[1]sPod%[1]s kind.
			- %[1]snamespaced%[1]s: The scope of group, version, and resource. Can be %[1]sbool%[1]s, %[1]strue%[1]s, or %[1]sfalse%[1]s.

			To select root objects, use the %[1]s--root-expression%[1]s flag. When set, only objects that are directly
			or transitively reachable from root objects are shown. This flag uses the same variables
			as %[1]sobject_selector_expression%[1]s, and must return a boolean. Multiple values are joined with %[1]sOR%[1]s
			statements. If any match, the object is used as root.

			For more information about using [label selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors)
			and [field selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/field-selectors/) to select namespaces, see the Kubernetes documentation.

			### Advanced usage

			Apart from high-level ways to construct the query, this command enables
			you to construct and send the query using all underlying API features.
			To understand what is possible, and how to do it, see the
			[technical design doc](https://gitlab.com/gitlab-org/cluster-integration/gitlab-agent/-/blob/master/doc/graph_api.md)

			The user should have permission to access the agent project.
			For more information, see [Grant users Kubernetes access](https://docs.gitlab.com/user/clusters/agent/user_access/).
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Run the default query for agent 123
			glab cluster graph -R user/project -a 123

			# Show common resources from the core and RBAC groups
			glab cluster graph -R user/project -a 123 --core --rbac

			# Show certain resources
			glab cluster graph -R user/project -a 123 --resource=pods --resource=configmaps

			# Same as above, but more compact
			glab cluster graph -R user/project -a 123 -r={pods,configmaps}

			# Select a certain namespace
			glab cluster graph -R user/project -a 123 -n={my-ns,my-stuff}

			# Select namespaces with a certain label
			glab cluster graph -R user/project -a 123 --ns-label-selector environment=production

			# Pass a custom watch request from a file
			glab cluster graph -R user/project -a 123 --stdin < query.json

			# Show objects reachable from pod roots
			glab cluster graph -R user/project -a 123 --root-expression "resource == \"pods\""`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context())
		},
	}
	fl := graphCmd.Flags()
	fl.Int64VarP(&opts.agentID, "agent", "a", opts.agentID, "The numeric agent ID to connect to.")
	fl.StringVar(&opts.listenNet, "listen-net", opts.listenNet, "Network on which to listen for connections.")
	fl.StringVar(&opts.listenAddr, "listen-addr", opts.listenAddr, "Address to listen on.")
	fl.BoolVarP(&opts.logWatchRequest, "log-watch-request", "", opts.logWatchRequest, "Log watch request to stdout. Helpful for debugging.")

	fl.StringArrayVarP(&opts.nsNames, "namespace", "n", opts.nsNames, "Namespaces to watch. If not specified, all namespaces are watched with label and field selectors filtering.")
	fl.StringVarP(&opts.nsLabels, "ns-label-selector", "", opts.nsLabels, "Label selector to select namespaces.")
	fl.StringVarP(&opts.nsSelector, "ns-field-selector", "", opts.nsSelector, "Field selector to select namespaces.")
	fl.StringVarP(&opts.nsCEL, "ns-expression", "", opts.nsCEL, "CEL expression to select namespaces. Evaluated before a namespace is watched and on any updates for the namespace object.")

	fl.StringArrayVarP(&opts.rootsCEL, "root-expression", "", opts.rootsCEL, "CEL expression to select root objects. Requires GitLab and agent version 18.3 or later.")
	fl.BoolVarP(&opts.ignoreArcDirection, "ignore-arc-direction", "", opts.ignoreArcDirection, "Ignore arc direction when evaluating root connectivity. Requires GitLab and agent version 18.3 or later.")

	fl.StringArrayVarP(&opts.resources, "resource", "r", opts.resources, "Resources to watch. You can see the list of resources your cluster supports by running 'kubectl api-resources'.")
	fl.BoolVar(&opts.groupCore, "core", opts.groupCore, "Watch pods, secrets, configmaps, and serviceaccounts in the core/v1 group.")
	fl.BoolVar(&opts.groupBatch, "batch", opts.groupBatch, "Watch jobs and cronjobs in the batch/v1 group.")
	fl.BoolVar(&opts.groupApps, "apps", opts.groupApps, "Watch deployments, replicasets, daemonsets, and statefulsets in the apps/v1 group.")
	fl.BoolVar(&opts.groupRBAC, "rbac", opts.groupRBAC, "Watch roles and rolebindings in the rbac.authorization.k8s.io/v1 group.")
	fl.BoolVar(&opts.groupClusterRBAC, "cluster-rbac", opts.groupClusterRBAC, "Watch clusterroles and clusterrolebindings in the rbac.authorization.k8s.io/v1 group.")
	fl.BoolVar(&opts.groupCRD, "crd", opts.groupCRD, "Watch customresourcedefinitions in the apiextensions.k8s.io/v1 group.")
	fl.BoolVar(&opts.readQueryFromStdIn, "stdin", opts.readQueryFromStdIn, "Read watch request from standard input.")

	cobra.CheckErr(graphCmd.MarkFlagRequired("agent"))
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "namespace")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "ns-label-selector")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "ns-field-selector")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "ns-expression")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "root-expression")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "resource")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "core")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "batch")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "apps")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "rbac")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "cluster-rbac")
	graphCmd.MarkFlagsMutuallyExclusive("stdin", "crd")

	return graphCmd
}

func (o *options) run(ctx context.Context) error {
	// 1. Plumbing setup
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}
	client, err := o.apiClient(repo.RepoHost())
	if err != nil {
		return err
	}

	// 2. Check token type, before reading the credential renews an OAuth2 token
	// this command cannot use anyway.
	kind, err := client.CredentialKind()
	switch {
	case errors.Is(err, api.ErrUnsupportedAuthSource):
		return errUnsupportedToken
	case err != nil:
		return err
	case kind != api.CredentialPAT:
		return errUnsupportedToken
	}

	cred, err := client.Credential(ctx)
	if err != nil {
		return err
	}

	// 3. Read the watch request
	watchReq, err := o.constructWatchRequest()
	if err != nil {
		return err
	}

	if o.logWatchRequest {
		o.io.LogInfo(string(watchReq))
	}

	// 4. Construct API URL
	md, _, err := client.Lab().Metadata.GetMetadata(gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("GitLab metadata: %w", err)
	}
	graphAPIURL := md.KAS.ExternalK8SProxyURL
	if !strings.HasSuffix(graphAPIURL, "/") {
		graphAPIURL += "/"
	}
	graphAPIURL += "graph"

	// 5. Start the server
	srv := server{
		log:           slog.New(slog.NewTextHandler(o.io.StdErr, nil)),
		io:            o.io,
		httpClient:    client.HTTPClient(),
		graphAPIURL:   graphAPIURL,
		listenNet:     o.listenNet,
		listenAddr:    o.listenAddr,
		authorization: fmt.Sprintf("Bearer pat:%d:%s", o.agentID, cred.Token),
		watchRequest:  watchReq,
	}
	return srv.Run(ctx)
}

func (o *options) constructWatchRequest() ([]byte, error) {
	if o.readQueryFromStdIn {
		return o.readWatchRequestFromStdin()
	}

	q := o.maybeConstructWatchQueriesForGroups()
	q = append(q, o.maybeConstructWatchQueriesForResources()...)
	if len(q) == 0 {
		q = o.defaultWatchQueries()
	}

	req, err := json.Marshal(&watchGraphWebSocketRequest{ //nolint:forbidigo // websocket request body, not stdout
		Queries:    q,
		Namespaces: o.constructWatchNamespaces(),
		Roots:      o.maybeConstructWatchRoots(),
	})
	if err != nil {
		return nil, fmt.Errorf("JSON marshal: %w", err)
	}
	return req, nil
}

func (o *options) constructWatchNamespaces() *namespaces {
	if o.isNamespaceOptsEmpty() {
		return &namespaces{
			ObjectSelectorExpression: "name != 'kube-system'",
		}
	}
	return &namespaces{
		Names:                    o.nsNames,
		LabelSelector:            o.nsLabels,
		FieldSelector:            o.nsSelector,
		ObjectSelectorExpression: o.nsCEL,
	}
}

func (o *options) isNamespaceOptsEmpty() bool {
	return len(o.nsNames) == 0 && o.nsLabels == "" && o.nsSelector == "" && o.nsCEL == ""
}

func (o *options) readWatchRequestFromStdin() ([]byte, error) {
	req, err := io.ReadAll(o.io.In)
	if err != nil {
		return nil, fmt.Errorf("reading request from stdin: %w", err)
	}
	return req, nil
}

func (o *options) defaultWatchQueries() []query {
	return []query{
		{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == '' && version == 'v1' && (resource in ['pods', 'secrets', 'configmaps', 'serviceaccounts'])",
			},
		},
		{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'apps' && version == 'v1' && (resource in ['deployments', 'replicasets', 'daemonsets', 'statefulsets'])",
			},
		},
		{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'batch' && version == 'v1' && (resource in ['jobs', 'cronjobs'])",
			},
		},
		{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'rbac.authorization.k8s.io' && version == 'v1' && !(resource in ['clusterrolebindings', 'clusterroles'])",
			},
		},
	}
}

func (o *options) maybeConstructWatchQueriesForResources() []query {
	if len(o.resources) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("resource in [")
	for i, resource := range o.resources {
		if i == 0 {
			sb.WriteByte('\'')
		} else {
			sb.WriteString(",'")
		}
		sb.WriteString(resource)
		sb.WriteByte('\'')
	}
	sb.WriteByte(']')

	return []query{
		{
			Include: &queryInclude{
				ResourceSelectorExpression: sb.String(),
			},
		},
	}
}

func (o *options) maybeConstructWatchQueriesForGroups() []query {
	var q []query

	if o.groupCore {
		q = append(q, query{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == '' && version == 'v1' && (resource in ['pods', 'secrets', 'configmaps', 'serviceaccounts'])",
			},
		})
	}
	if o.groupBatch {
		q = append(q, query{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'batch' && version == 'v1' && (resource in ['jobs', 'cronjobs'])",
			},
		})
	}
	if o.groupApps {
		q = append(q, query{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'apps' && version == 'v1' && (resource in ['deployments', 'replicasets', 'daemonsets', 'statefulsets'])",
			},
		})
	}
	if o.groupRBAC {
		q = append(q, query{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'rbac.authorization.k8s.io' && version == 'v1' && (resource in ['roles', 'rolebindings'])",
			},
		})
	}
	if o.groupClusterRBAC {
		q = append(q, query{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'rbac.authorization.k8s.io' && version == 'v1' && (resource in ['clusterroles', 'clusterrolebindings'])",
			},
		})
	}
	if o.groupCRD {
		q = append(q, query{
			Include: &queryInclude{
				ResourceSelectorExpression: "group == 'apiextensions.k8s.io' && version == 'v1' && resource == 'customresourcedefinitions'",
			},
		})
	}
	return q
}

func (o *options) maybeConstructWatchRoots() *roots {
	if len(o.rootsCEL) == 0 {
		return nil
	}
	var arcsToIgnoreDirection []string
	if o.ignoreArcDirection {
		arcsToIgnoreDirection = []string{string(ownerReferenceArcType), string(referenceArcType), string(transitiveReferenceArcType)}
	}
	return &roots{
		ObjectSelectorExpressions: o.rootsCEL,
		IgnoreArcDirection:        arcsToIgnoreDirection,
	}
}
