// Package start implements `glab autoflow workflow start`.
package start

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/autoflow"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/autoflow/autoflowutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	conn           autoflowutils.Connection
	script         string
	scriptFile     string
	idempotencyKey string
	tokenBinding   *cmdutils.SecretFile
	kwargs         []string
	channels       []string
	outputFormat   string

	decodedTokenBinding []byte
	parsedKwargs        []*autoflowv1.Kwarg
}

// NewCmd returns the `glab autoflow workflow start` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start an AutoFlow workflow. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Submit a Starlark workflow definition to the AutoFlow service of GitLab Relay and start a workflow for it. The command returns once the workflow is persisted and does not wait for it to run.

			The command exchanges your GitLab credential for a short-lived token for AutoFlow, so your user needs the %[1]stoken_exchange_autoflow_audience%[1]s feature flag on the GitLab instance. By default, the workflow belongs to the namespace of your organization.

			%[1]s--kwarg%[1]s passes a keyword argument to the workflow's %[1]smain%[1]s function as JSON: %[1]snull%[1]s becomes %[1]sNone%[1]s, a number without a fraction or exponent an integer, an array a list and an object a dict. Sensitive values are not supported yet. %[1]s--channel%[1]s creates a channel and passes it as the keyword argument of the same name, so %[1]sglab autoflow workflow send%[1]s can send to it.

			The output contains the workflow token, the token binding and a token for each channel. All of them are credentials:

			- The workflow key and workflow token read or cancel the workflow later with %[1]sglab autoflow workflow get%[1]s and %[1]sglab autoflow workflow cancel%[1]s.
			- The idempotency key and token binding re-submit the same workflow to get fresh tokens. Pass them with %[1]s--idempotency-key%[1]s and %[1]s--token-binding-file%[1]s, or the token binding in its environment variable. A re-submission returns the existing workflow and ignores the definition and the keyword arguments. A key without its binding gets %[1]sPermissionDenied%[1]s. It returns channel tokens for the channels it passes again.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Start the workflow in main.star on Runway staging
			glab autoflow workflow start --hostname staging.gitlab.com \
			  --address staging.autoflow-gke.svc.gitlab.net:443 \
			  --script-file main.star

			# Start a workflow with a keyword argument and a channel
			glab autoflow workflow start --address relay.example.com:443 \
			  --script 'def main(w, count, inbox): return [gather(inbox) for _ in range(count)]' \
			  --kwarg count=2 --channel inbox

			# Re-submit a workflow to get fresh tokens for it
			glab autoflow workflow start --address relay.example.com:443 \
			  --idempotency-key glab-T3KQZ7WMXH4RJ2NVB6YLC5DPEA \
			  --token-binding-file token-binding.txt --script-file main.star

			# Start an inline workflow and print the result as JSON
			glab autoflow workflow start --address relay.example.com:443 \
			  --script 'def main(w): return "hello"' --output json
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		// The output carries the workflow, channel and binding tokens.
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	opts.conn.AddFlags(cmd)
	fl := cmd.Flags()
	fl.StringVar(&opts.script, "script", "", "Workflow definition as Starlark source.")
	fl.StringVar(&opts.scriptFile, "script-file", "", "Path to a file with the workflow definition. Use - to read from standard input.")
	fl.StringVar(&opts.idempotencyKey, "idempotency-key", "", "Key that deduplicates submissions within the namespace. Defaults to a random key. Without a token binding, the key starts a new workflow, and Relay refuses a key that an earlier workflow uses.")
	fl.StringArrayVar(&opts.kwargs, "kwarg", nil, "Keyword argument of the workflow's main function, as <name>=<JSON value>. Sensitive values are not supported. Repeat for more arguments.")
	fl.StringArrayVar(&opts.channels, "channel", nil, "Name of a channel to create and pass to the workflow's main function as the keyword argument of the same name. Repeat for more channels.")
	opts.tokenBinding = autoflowutils.NewTokenBindingFlag(cmd)
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	cmd.MarkFlagsOneRequired("script", "script-file")
	cmd.MarkFlagsMutuallyExclusive("script", "script-file")
	cobra.CheckErr(cmd.MarkFlagFilename("script-file", "star"))

	return cmd
}

func (o *options) complete() error {
	if err := o.conn.Complete(); err != nil {
		return err
	}

	if o.tokenBinding.FromStdin() && o.scriptFile == "-" {
		return &cmdutils.FlagError{Err: errors.New("only one of --script-file and --token-binding-file can read from standard input")}
	}
	if o.tokenBinding.IsSet() {
		// A binding without the key it was registered under cannot reach its
		// workflow, so it would only be shared between new ones.
		if o.idempotencyKey == "" {
			return &cmdutils.FlagError{Err: errors.New("a token binding re-submits an earlier workflow, so it needs the --idempotency-key of that workflow")}
		}
		encoded, err := o.tokenBinding.Read(o.io.In)
		if err != nil {
			return err
		}
		o.decodedTokenBinding, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing the token binding: %w", err)}
		}
	} else {
		o.decodedTokenBinding = make([]byte, autoflow.TokenBindingSize)
		if _, err := rand.Read(o.decodedTokenBinding); err != nil {
			return fmt.Errorf("failed to generate token binding: %w", err)
		}
	}

	if o.idempotencyKey == "" {
		o.idempotencyKey = "glab-" + rand.Text()
	}

	return o.completeKwargs()
}

func (o *options) completeKwargs() error {
	seen := map[string]struct{}{}
	add := func(name string, value *autoflowv1.Value) error {
		if name == "" {
			return &cmdutils.FlagError{Err: errors.New("a keyword argument needs a name")}
		}
		if _, ok := seen[name]; ok {
			return &cmdutils.FlagError{Err: fmt.Errorf("keyword argument %q is passed more than once", name)}
		}
		seen[name] = struct{}{}
		o.parsedKwargs = append(o.parsedKwargs, &autoflowv1.Kwarg{Name: name, Value: value})
		return nil
	}

	for _, kwarg := range o.kwargs {
		name, raw, ok := strings.Cut(kwarg, "=")
		if !ok {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --kwarg %s: want <name>=<JSON value>", kwarg)}
		}
		value, err := autoflow.ParseValue([]byte(raw))
		if err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --kwarg %s: %w", name, err)}
		}
		if err := add(name, value); err != nil {
			return err
		}
	}
	for _, channel := range o.channels {
		if err := add(channel, autoflow.ChannelValue(channel)); err != nil {
			return err
		}
	}
	return nil
}

func (o *options) validate() error {
	if len(o.decodedTokenBinding) != autoflow.TokenBindingSize {
		return &cmdutils.FlagError{Err: fmt.Errorf("error parsing the token binding: must decode to %d bytes, got %d", autoflow.TokenBindingSize, len(o.decodedTokenBinding))}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	definition, err := o.readDefinition()
	if err != nil {
		return err
	}
	client, conn, err := o.conn.Dial(ctx, o.io, o.apiClient)
	if err != nil {
		return err
	}
	defer conn.Close()

	tokenBinding := base64.StdEncoding.EncodeToString(o.decodedTokenBinding)
	ctx, cancel := context.WithTimeout(ctx, autoflowutils.CallTimeout)
	defer cancel()
	resp, err := client.StartWorkflow(ctx, &autoflowrpcv1.StartWorkflowRequest{
		IdempotencyKey:     o.idempotencyKey,
		WorkflowDefinition: definition,
		NamespaceId:        o.conn.NamespaceID[:],
		TokenBinding:       o.decodedTokenBinding,
		Kwargs:             o.parsedKwargs,
	})
	if err != nil {
		if !mayHavePersisted(err) {
			return fmt.Errorf("failed to start workflow: %w", err)
		}
		return o.retryableError(tokenBinding, err)
	}

	return o.print(started{
		WorkflowKey:    resp.GetWorkflowKey(),
		WorkflowToken:  resp.GetWorkflowToken(),
		NamespaceID:    o.conn.NamespaceID.String(),
		IdempotencyKey: o.idempotencyKey,
		TokenBinding:   tokenBinding,
		ChannelTokens:  channelTokens(resp.GetChannelTokens()),
	})
}

// mayHavePersisted reports whether Relay may have stored the workflow before
// the call failed with err. Only the same key and binding can reach it then.
// With the other codes, Relay refused the request without storing a workflow
// for it.
func mayHavePersisted(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.Unknown, codes.Internal, codes.Aborted:
		return true
	default:
		return false
	}
}

func (o *options) retryableError(tokenBinding string, err error) error {
	var binding string
	switch file := o.tokenBinding.File(); {
	case file != "" && file != "-":
		binding = fmt.Sprintf("--token-binding-file %q", file)
	case file == "" && o.tokenBinding.IsSet():
		binding = "keep " + o.tokenBinding.Env() + " set"
	default:
		// A generated binding, or one read from standard input, exists only
		// in this process.
		saved, saveErr := saveTokenBinding(tokenBinding)
		if saveErr != nil {
			return fmt.Errorf("failed to start workflow, and failed to save the token binding to retry it: %w", errors.Join(err, saveErr))
		}
		binding = fmt.Sprintf("--token-binding-file %q", saved)
	}
	return fmt.Errorf("failed to start workflow (to retry without starting a second one, pass --idempotency-key %s and %s): %w", o.idempotencyKey, binding, err)
}

func saveTokenBinding(tokenBinding string) (string, error) {
	f, err := os.CreateTemp("", "glab-autoflow-token-binding-")
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(tokenBinding + "\n")
	if err = errors.Join(err, f.Close()); err != nil {
		return "", errors.Join(err, os.Remove(f.Name()))
	}
	return f.Name(), nil
}

func (o *options) readDefinition() ([]byte, error) {
	definition, err := o.readDefinitionSource()
	if err != nil {
		return nil, err
	}
	if len(definition) == 0 {
		return nil, fmt.Errorf("the workflow definition is empty")
	}
	return definition, nil
}

func (o *options) readDefinitionSource() ([]byte, error) {
	switch o.scriptFile {
	case "":
		return []byte(o.script), nil
	case "-":
		b, err := io.ReadAll(o.io.In)
		if err != nil {
			return nil, fmt.Errorf("failed to read the workflow definition from standard input: %w", err)
		}
		return b, nil
	default:
		b, err := os.ReadFile(o.scriptFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read the workflow definition: %w", err)
		}
		return b, nil
	}
}

type started struct {
	WorkflowKey    string         `json:"workflow_key"`
	WorkflowToken  string         `json:"workflow_token"`
	NamespaceID    string         `json:"namespace_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	TokenBinding   string         `json:"token_binding"`
	ChannelTokens  []channelToken `json:"channel_tokens,omitempty"`
}

type channelToken struct {
	Channel string `json:"channel"`
	Token   string `json:"token"`
}

func channelTokens(tokens []*autoflowv1.ChannelToken) []channelToken {
	out := make([]channelToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, channelToken{Channel: t.GetChannelName(), Token: t.GetToken()})
	}
	return out
}

func (o *options) print(s started) error {
	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(s)
	default:
		_, err := fmt.Fprintf(o.io.StdOut, //nolint:forbidigo // the credentials must reach the caller or the command fails
			"workflow_key: %s\nworkflow_token: %s\nnamespace_id: %s\nidempotency_key: %s\ntoken_binding: %s\n",
			s.WorkflowKey, s.WorkflowToken, s.NamespaceID, s.IdempotencyKey, s.TokenBinding)
		if err != nil {
			return err
		}
		for _, t := range s.ChannelTokens {
			if _, err := fmt.Fprintf(o.io.StdOut, "channel_token.%s: %s\n", t.Channel, t.Token); err != nil { //nolint:forbidigo // the credentials must reach the caller or the command fails
				return err
			}
		}
		return nil
	}
}
