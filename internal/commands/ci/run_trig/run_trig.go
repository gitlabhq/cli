package run_trig

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var envVariables = []string{}

func parseVarArg(s string) (string, string, error) {
	// From https://pkg.go.dev/strings#Split:
	//
	// > If s does not contain sep and sep is not empty,
	// > Split returns a slice of length 1 whose only element is s.
	//
	// Therefore, the function will always return a slice of min length 1.
	v := strings.SplitN(s, ":", 2)
	if len(v) == 1 {
		return "", "", fmt.Errorf("invalid argument structure")
	}
	key := strings.TrimSpace(v[0])
	if key == "" {
		return "", "", fmt.Errorf("variable key cannot be empty")
	}
	return key, v[1], nil
}

func NewCmdRunTrig(f cmdutils.Factory) *cobra.Command {
	pipelineRunCmd := &cobra.Command{
		Use:     "run-trig [flags]",
		Short:   `Run a CI/CD pipeline trigger.`,
		Aliases: []string{},
		Long: heredoc.Docf(`
			Use %[1]s--token%[1]s to provide a pipeline trigger token. If omitted, the
			%[1]sCI_JOB_TOKEN%[1]s environment variable must be set.

			Use %[1]s--branch%[1]s to specify a branch or reference. Defaults to the current branch.
		`, "`") + "\n" + cmdutils.PipelineInputsDescription,
		Example: heredoc.Doc(`
			glab ci run-trig -t xxxx
			glab ci run-trig -t xxxx -b main

			# Specify CI variables
			glab ci run-trig -t xxxx -b main --variables key1:val1
			glab ci run-trig -t xxxx -b main --variables key1:val1,key2:val2
			glab ci run-trig -t xxxx -b main --variables key1:val1 --variables key2:val2

			# Specify CI inputs
			glab ci run-trig -t xxxx -b main --input key1:val1 --input key2:val2
			glab ci run-trig -t xxxx -b main --input "replicas:int(3)" --input "debug:bool(false)" --input "regions:array(us-east,eu-west)"`),
		Args: cobra.ExactArgs(0),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			pipelineVariables := make(map[string]string)

			if customPipelineVars, _ := cmd.Flags().GetStringSlice("variables"); len(customPipelineVars) > 0 {
				for _, v := range customPipelineVars {
					key, val, err := parseVarArg(v)
					if err != nil {
						return fmt.Errorf("parsing pipeline variable. Expected format KEY:VALUE: %w", err)
					}
					pipelineVariables[key] = val
				}
			}

			pipelineInputs, err := cmdutils.PipelineInputsFromFlags(cmd)
			if err != nil {
				return err
			}

			c := &gitlab.RunPipelineTriggerOptions{
				Inputs: pipelineInputs,
			}

			if len(pipelineVariables) != 0 {
				c.Variables = pipelineVariables
			}

			branch, err := cmd.Flags().GetString("branch")
			if err != nil {
				return err
			}
			if branch != "" {
				c.Ref = new(branch)
			} else if currentBranch, err := f.Branch(); err == nil {
				c.Ref = new(currentBranch)
			} else {
				// `ci run-trig` is running out of a git repo
				f.IO().LogInfo("not in a Git repository. Using repository argument.")
				c.Ref = new(ciutils.GetDefaultBranch(repo, client))
			}

			token, err := cmd.Flags().GetString("token")
			if err != nil {
				return err
			}
			if token == "" {
				token = os.Getenv("CI_JOB_TOKEN")
			}
			if token == "" {
				return errors.New("`--token` parameter can be omitted only if `CI_JOB_TOKEN` environment variable is set")
			}
			c.Token = new(token)

			pipe, _, err := client.PipelineTriggers.RunPipelineTrigger(repo.FullName(), c)
			if err != nil {
				return err
			}

			output := fmt.Sprintf("Created pipeline (ID: %d), status: %s, ref: %s, weburl: %s", pipe.ID, pipe.Status, pipe.Ref, pipe.WebURL)
			f.IO().LogInfo(output)

			return nil
		},
	}
	pipelineRunCmd.Flags().StringP("token", "t", "", "Pipeline trigger token. Can be omitted only if the 'CI_JOB_TOKEN' environment variable is set.")
	pipelineRunCmd.Flags().StringP("branch", "b", "", "Create pipeline on branch or reference <string>.")
	pipelineRunCmd.Flags().StringSliceVarP(&envVariables, "variables", "", []string{}, "Pass variables to pipeline in the format <key>:<value>. Multiple variables can be comma-separated or specified by repeating the flag.")
	cmdutils.AddPipelineInputsFlag(pipelineRunCmd)

	return pipelineRunCmd
}
