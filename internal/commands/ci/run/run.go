package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func parseVarArg(s string) (*gitlab.PipelineVariableOptions, error) {
	// From https://pkg.go.dev/strings#Split:
	//
	// > If s does not contain sep and sep is not empty,
	// > Split returns a slice of length 1 whose only element is s.
	//
	// Therefore, the function will always return a slice of min length 1.
	v := strings.SplitN(s, ":", 2)
	if len(v) == 1 {
		return nil, fmt.Errorf("invalid argument structure")
	}
	key := strings.TrimSpace(v[0])
	if key == "" {
		return nil, fmt.Errorf("variable key cannot be empty")
	}
	return &gitlab.PipelineVariableOptions{
		Key:   &key,
		Value: &v[1],
	}, nil
}

func extractEnvVar(s string) (*gitlab.PipelineVariableOptions, error) {
	pvar, err := parseVarArg(s)
	if err != nil {
		return nil, err
	}
	pvar.VariableType = new(gitlab.EnvVariableType)
	return pvar, nil
}

func extractFileVar(s string) (*gitlab.PipelineVariableOptions, error) {
	pvar, err := parseVarArg(s)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(*pvar.Value)
	if err != nil {
		return nil, err
	}
	pvar.VariableType = new(gitlab.FileVariableType)
	pvar.Value = new(string(b))
	return pvar, nil
}

type PipelineData struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Ref    string `json:"ref"`
	WebURL string `json:"web_url"`
}

func createPipeline(cmd *cobra.Command, c *gitlab.CreatePipelineOptions, f cmdutils.Factory, apiClient *gitlab.Client, repo glrepo.Interface, mr bool) (*PipelineData, error) {
	branch, err := resolveBranch(cmd, f)
	if err != nil {
		return nil, err
	}
	if mr {
		pipe, err := createMrPipeline(cmd.Context(), branch, f, apiClient, repo)
		if err != nil {
			return nil, fmt.Errorf("could not create mr pipeline for branch %s: %w", branch, err)
		}
		return &PipelineData{
			ID:     pipe.ID,
			Status: pipe.Status,
			Ref:    pipe.Ref,
			WebURL: pipe.WebURL,
		}, nil
	}
	pipe, err := createBranchPipeline(branch, c, apiClient, repo)
	if err != nil {
		return nil, fmt.Errorf("could not create branch pipeline for branch %s: %w", branch, err)
	}
	return &PipelineData{
		ID:     pipe.ID,
		Status: pipe.Status,
		Ref:    pipe.Ref,
		WebURL: pipe.WebURL,
	}, nil
}

func createBranchPipeline(branch string, c *gitlab.CreatePipelineOptions, apiClient *gitlab.Client, repo glrepo.Interface) (*gitlab.Pipeline, error) {
	c.Ref = new(branch)
	pipe, _, err := apiClient.Pipelines.CreatePipeline(repo.FullName(), c)
	return pipe, err
}

func resolveBranch(cmd *cobra.Command, f cmdutils.Factory) (string, error) {
	br, err := cmd.Flags().GetString("branch")
	if err != nil {
		return "", err
	}
	var branch string
	if br != "" {
		branch = br
	} else if currentBranch, err := f.Branch(); err == nil {
		branch = currentBranch
	} else {
		// `ci run` is running out of a git repo
		f.IO().LogInfo("not in a Git repository. Using repository argument.")
		client, err := f.GitLabClient()
		if err != nil {
			return "", err
		}
		repo, err := f.BaseRepo()
		if err != nil {
			return "", err
		}
		branch = ciutils.GetDefaultBranch(repo, client)
	}
	return branch, nil
}

func createMrPipeline(ctx context.Context, branch string, f cmdutils.Factory, apiClient *gitlab.Client, repo glrepo.Interface) (*gitlab.PipelineInfo, error) {
	mr, err := mrutils.GetMRForBranch(
		ctx,
		f.IO(),
		apiClient,
		mrutils.MrOptions{
			BaseRepo: repo, Branch: branch, State: "opened", PromptEnabled: f.IO().PromptEnabled(),
		},
	)
	if err != nil {
		return nil, err
	}

	pipe, _, err := apiClient.MergeRequests.CreateMergeRequestPipeline(repo.FullName(), mr.IID)
	if err != nil {
		return nil, err
	}
	return pipe, nil
}

func resolvePipelineVars(cmd *cobra.Command) ([]*gitlab.PipelineVariableOptions, error) {
	pipelineVars := []*gitlab.PipelineVariableOptions{}
	for _, flag := range []string{"variables-env", "variables"} {
		if customPipelineVars, _ := cmd.Flags().GetStringSlice(flag); len(customPipelineVars) > 0 {
			for _, v := range customPipelineVars {
				pvar, err := extractEnvVar(v)
				if err != nil {
					return nil, fmt.Errorf("parsing pipeline variable. Expected format KEY:VALUE: %w", err)
				}
				pipelineVars = append(pipelineVars, pvar)
			}
		}
	}

	if customPipelineFileVars, _ := cmd.Flags().GetStringSlice("variables-file"); len(customPipelineFileVars) > 0 {
		for _, v := range customPipelineFileVars {
			pvar, err := extractFileVar(v)
			if err != nil {
				return nil, fmt.Errorf("parsing pipeline variable. Expected format KEY:FILENAME: %w", err)
			}
			pipelineVars = append(pipelineVars, pvar)
		}
	}

	vf, err := cmd.Flags().GetString("variables-from")
	if err != nil {
		return nil, err
	}

	if vf != "" {
		b, err := os.ReadFile(vf)
		if err != nil {
			return nil, fmt.Errorf("opening variable file: %s", vf)
		}
		var result []*gitlab.PipelineVariableOptions
		err = json.Unmarshal(b, &result)
		if err != nil {
			return nil, fmt.Errorf("loading pipeline values: %w. Expected JSON array format: [{\"key\": \"VAR_NAME\", \"value\": \"VAR_VALUE\", \"variable_type\": \"env_var\"}]", err)
		}
		pipelineVars = append(pipelineVars, result...)
	}

	return pipelineVars, nil
}

func NewCmdRun(f cmdutils.Factory) *cobra.Command {
	openInBrowser := false
	mr := false

	pipelineRunCmd := &cobra.Command{
		Use:     "run [flags]",
		Short:   `Create a new CI/CD pipeline.`,
		Aliases: []string{"create"},
		Long: heredoc.Docf(`
			Use %[1]s--branch%[1]s to specify a branch or reference. Defaults to the current branch.

			The variable flags (%[1]s--variables%[1]s, %[1]s--variables-env%[1]s,
			%[1]s--variables-file%[1]s, %[1]s--variables-from%[1]s) cannot be used with
			%[1]s--mr%[1]s. If both are used, the command fails.
		`, "`") + "\n" + cmdutils.PipelineInputsDescription,
		Example: heredoc.Doc(`
			glab ci run
			glab ci run --variables \"key1:value,with,comma\"
			glab ci run -b main
			glab ci run --web
			glab ci run --mr

			# Specify CI variables
			glab ci run -b main --variables-env key1:val1
			glab ci run -b main --variables-env key1:val1,key2:val2
			glab ci run -b main --variables-env key1:val1 --variables-env key2:val2
			glab ci run -b main --variables-file MYKEY:file1 --variables KEY2:some_value

			# Specify CI inputs
			glab ci run -b main --input key1:val1 --input key2:val2
			glab ci run -b main --input "replicas:int(3)" --input "debug:bool(false)" --input "regions:array(us-east,eu-west)"

			# Load variables from JSON file
			# Create variables.json with this format:
			# [
			#   {
			#     "key": "CI_PIPELINE_SOURCE",
			#     "value": "web",
			#     "variable_type": "env_var"
			#   },
			#   {
			#     "key": "DEPLOY_ENV",
			#     "value": "production"
			#   }
			# ]
			glab ci run -b main --variables-from variables.json`),
		Args: cobra.NoArgs,
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

			pipelineVars, err := resolvePipelineVars(cmd)
			if err != nil {
				return err
			}

			pipelineInputs, err := cmdutils.PipelineInputsFromFlags(cmd)
			if err != nil {
				return err
			}

			c := &gitlab.CreatePipelineOptions{
				Inputs: pipelineInputs,
			}

			if len(pipelineVars) != 0 {
				c.Variables = new(pipelineVars)
			}

			pipe, err := createPipeline(cmd, c, f, client, repo, mr)
			if err != nil {
				return err
			}
			if openInBrowser { // open in browser if --web flag is specified
				webURL := pipe.WebURL

				if f.IO().IsOutputTTY() {
					f.IO().LogErrorf("Opening %s in your browser.\n", utils.DisplayURL(webURL))
				}

				cfg := f.Config()

				browser, _ := cfg.Get(repo.RepoHost(), "browser")
				return utils.OpenInBrowser(webURL, browser)
			}

			output := fmt.Sprintf("Created pipeline (id: %d), status: %s, ref: %s, weburl: %s", pipe.ID, pipe.Status, pipe.Ref, pipe.WebURL)
			f.IO().LogInfo(output)
			return nil
		},
	}
	pipelineRunCmd.Flags().StringP("branch", "b", "", "Create pipeline on branch or reference <string>.")
	pipelineRunCmd.Flags().StringSliceP("variables", "", []string{}, "Pass variables to the pipeline in the format <key>:<value>. Cannot be used for MR pipelines.")
	pipelineRunCmd.Flags().StringSliceP("variables-env", "", []string{}, "Pass variables to the pipeline in the format <key>:<value>. Cannot be used for MR pipelines.")
	pipelineRunCmd.Flags().StringSliceP("variables-file", "", []string{}, "Pass file contents as a file variable to the pipeline in the format <key>:<filename>. Cannot be used for MR pipelines.")
	pipelineRunCmd.Flags().StringP("variables-from", "f", "", "JSON file with variables for pipeline execution. Expects array of hashes, each with at least 'key' and 'value'. Cannot be used for MR pipelines.")
	pipelineRunCmd.Flags().BoolVarP(&openInBrowser, "web", "w", false, "Open pipeline in a browser. Uses default browser, or browser specified in BROWSER environment variable.")
	pipelineRunCmd.Flags().BoolVar(&mr, "mr", false, "Run merge request pipeline instead of branch pipeline.")
	cmdutils.AddPipelineInputsFlag(pipelineRunCmd)

	for _, flag := range []string{"variables", "variables-env", "variables-file", "variables-from", "input"} {
		// https://docs.gitlab.com/api/merge_requests/#create-merge-request-pipeline
		// MR pipeline creation API does not accept variables unlike "normal" pipelines
		// https://docs.gitlab.com/api/pipelines/#create-a-new-pipeline
		pipelineRunCmd.MarkFlagsMutuallyExclusive("mr", flag)
	}

	return pipelineRunCmd
}
