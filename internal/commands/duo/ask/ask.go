package ask

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/shlex"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/run"
)

type request struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
}

type response struct {
	Predictions []struct {
		Candidates []struct {
			Content string `json:"content"`
		} `json:"candidates"`
	} `json:"predictions"`
}

type result struct {
	Commands    []string `json:"commands"`
	Explanation string   `json:"explanation"`
}

type opts struct {
	Prompt    string
	IO        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	Git       bool
	BaseRepo  func() (glrepo.Interface, error)
}

var (
	cmdHighlightRegexp = regexp.MustCompile("`+\n?([^`]*)\n?`+\n?")
	cmdExecRegexp      = regexp.MustCompile("```([^`]*)```")
	vertexAI           = "vertexai"
)

const (
	runCmdsQuestion   = "Would you like to run these Git commands?"
	gitCmd            = "git"
	gitCmdAPIPath     = "ai/llm/git_command"
	spinnerText       = "Generating Git commands..."
	aiResponseErr     = "AI response has not been generated correctly"
	apiUnreachableErr = "API is unreachable"
)

func NewCmdAsk(f cmdutils.Factory) *cobra.Command {
	opts := &opts{
		IO:        f.IO(),
		apiClient: f.ApiClient,
		BaseRepo:  f.BaseRepo,
	}

	duoAskCmd := &cobra.Command{
		Use:    "ask <prompt>",
		Short:  "Generate Git commands from natural language.",
		Hidden: true,
		Long: heredoc.Doc(`
			Generate Git commands from natural language using AI assistance.

			Describe what you want to do in plain language, and GitLab Duo suggests
			the appropriate Git commands. You receive both the commands and an explanation.

			After you receive the suggested commands, you can run them directly from
			the CLI. You must confirm each command before it runs.

			Use this command to:

			- Retrieve forgotten Git commands.
			- Learn how to accomplish specific Git operations.
			- Get guidance on complex Git workflows.
			- Reduce context switching during development.
		`),
		Example: heredoc.Doc(`
			# Explain how to display the titles of the last 10 commits
			glab duo ask how to list last 10 commit titles

			# Explain how to undo the last commit
			glab duo ask how do I undo my last commit

			# Explain how to display files changed in the last commit
			glab duo ask show me files changed in the last commit`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !opts.Git {
				return nil
			}

			if len(args) == 0 {
				return nil
			}

			opts.Prompt = strings.Join(args, " ")

			result, err := opts.Result()
			if err != nil {
				return err
			}

			opts.displayResult(result)

			if len(result.Commands) > 0 {
				if err := opts.executeCommands(cmd.Context(), result.Commands); err != nil {
					return err
				}
			}
			return nil
		},
	}

	duoAskCmd.Flags().BoolVarP(&opts.Git, "git", "", true, "Ask a question about Git.")

	return duoAskCmd
}

func (opts *opts) Result() (*result, error) {
	opts.IO.StartSpinner(spinnerText)
	defer opts.IO.StopSpinner("")

	var repoHost string
	if baseRepo, err := opts.BaseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}

	c, err := opts.apiClient(repoHost)
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to get HTTP client.")
	}
	client := c.Lab()

	body := request{Prompt: opts.Prompt, Model: vertexAI}
	request, err := client.NewRequest(http.MethodPost, gitCmdAPIPath, body, nil)
	if err != nil {
		return nil, cmdutils.WrapError(err, "failed to create a request.")
	}

	var r response
	_, err = client.Do(request, &r)
	if err != nil {
		return nil, cmdutils.WrapError(err, apiUnreachableErr)
	}

	if len(r.Predictions) == 0 || len(r.Predictions[0].Candidates) == 0 {
		return nil, errors.New(aiResponseErr)
	}

	content := r.Predictions[0].Candidates[0].Content

	var cmds []string
	for _, cmd := range cmdExecRegexp.FindAllString(content, -1) {
		cmds = append(cmds, strings.Trim(cmd, "\n`"))
	}

	return &result{
		Commands:    cmds,
		Explanation: content,
	}, nil
}

func (opts *opts) displayResult(result *result) {
	color := opts.IO.Color()

	opts.IO.LogInfo(color.Bold("Commands:\n"))

	for _, cmd := range result.Commands {
		opts.IO.LogInfo(color.Green(cmd))
	}

	opts.IO.LogInfo(color.Bold("\nExplanation:\n"))
	explanation := cmdHighlightRegexp.ReplaceAllString(result.Explanation, color.Green("$1"))
	opts.IO.LogInfo(explanation + "\n")
}

func (opts *opts) executeCommands(ctx context.Context, commands []string) error {
	color := opts.IO.Color()

	confirmed := true
	question := color.Bold(runCmdsQuestion)
	if err := opts.IO.Confirm(ctx, &confirmed, question); err != nil {
		return err
	}

	if !confirmed {
		return nil
	}

	for _, command := range commands {
		if err := opts.executeCommand(ctx, command); err != nil {
			return err
		}
	}

	return nil
}

func (opts *opts) executeCommand(ctx context.Context, cmd string) error {
	gitArgs, err := shlex.Split(cmd)
	if err != nil {
		return nil
	}

	if gitArgs[0] != gitCmd {
		return nil
	}

	color := opts.IO.Color()
	question := fmt.Sprintf("Run `%s`", color.Green(cmd))
	confirmed := true
	if err := opts.IO.Confirm(ctx, &confirmed, question); err != nil {
		return err
	}

	if !confirmed {
		return nil
	}

	execCmd := exec.CommandContext(ctx, "git", gitArgs[1:]...)
	output, err := run.PrepareCmd(execCmd).Output()
	if err != nil {
		return err
	}

	if len(output) == 0 {
		return nil
	}

	if err := opts.IO.StartPager(); err != nil {
		return fmt.Errorf("failed to start pager: %w", err)
	}
	defer opts.IO.StopPager()

	opts.IO.LogInfo(string(output))

	return nil
}
