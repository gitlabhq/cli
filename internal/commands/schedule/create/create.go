package create

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var variableList []string

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	scheduleCreateCmd := &cobra.Command{
		Use:   "create [flags]",
		Short: `Create a new pipeline schedule.`,
		Long: heredoc.Docf(`
		Create a new CI/CD pipeline schedule. The %[1]s--cron%[1]s, %[1]s--description%[1]s, and %[1]s--ref%[1]s flags
		are required:
		
		- %[1]s--cron%[1]s sets the schedule's recurrence in cron syntax.
		- %[1]s--ref%[1]s sets the branch or tag the pipeline runs against.
		- %[1]s--description%[1]s provides a human-readable label.

		Use %[1]s--variable%[1]s to add pipeline variables in %[1]skey:value%[1]s format.
		Pass %[1]s--variable%[1]s multiple times to add several variables.

		By default, the schedule is created in the current project. Use
		%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Create a scheduled pipeline that runs every hour
			glab schedule create --cron "0 * * * *" --description "Hourly build" --ref main

			# Create a schedule with pipeline variables
			glab schedule create --cron "0 0 * * *" --description "Daily build" --ref main --variable "foo:bar" --variable "baz:qux"
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			l := &gitlab.CreatePipelineScheduleOptions{}

			description, _ := cmd.Flags().GetString("description")
			ref, _ := cmd.Flags().GetString("ref")
			cron, _ := cmd.Flags().GetString("cron")
			cronTimeZone, _ := cmd.Flags().GetString("cronTimeZone")
			active, _ := cmd.Flags().GetBool("active")
			variableList, _ = cmd.Flags().GetStringSlice("variable")
			variables := make([]*gitlab.CreatePipelineScheduleVariableOptions, 0, len(variableList))
			for _, v := range variableList {
				key, value, ok := strings.Cut(v, ":")
				if !ok {
					return fmt.Errorf("invalid format for --variable: %s", v)
				}
				variables = append(variables, &gitlab.CreatePipelineScheduleVariableOptions{
					Key:   &key,
					Value: &value,
				})
			}

			l.Description = &description
			l.Ref = &ref
			l.Cron = &cron
			l.CronTimezone = &cronTimeZone
			l.Active = &active

			schedule, _, err := client.PipelineSchedules.CreatePipelineSchedule(repo.FullName(), l, gitlab.WithContext(cmd.Context()))
			if err != nil {
				return err
			}

			for _, variable := range variables {
				_, _, err := client.PipelineSchedules.CreatePipelineScheduleVariable(repo.FullName(), schedule.ID, variable, gitlab.WithContext(cmd.Context()))
				if err != nil {
					return err
				}
			}

			f.IO().LogInfo("Created schedule with ID", schedule.ID)

			return nil
		},
	}
	scheduleCreateCmd.Flags().String("description", "", "Description of the schedule.")
	scheduleCreateCmd.Flags().String("ref", "", "Target branch or tag.")
	scheduleCreateCmd.Flags().String("cron", "", "Cron interval pattern.")
	scheduleCreateCmd.Flags().String("cronTimeZone", "UTC", "Cron timezone.")
	scheduleCreateCmd.Flags().Bool("active", true, "Whether or not the schedule is active.")
	scheduleCreateCmd.Flags().StringSliceVar(&variableList, "variable", []string{}, "Pass variables to schedule in the format <key>:<value>. Repeat flag for multiple variables.")

	_ = scheduleCreateCmd.MarkFlagRequired("ref")
	_ = scheduleCreateCmd.MarkFlagRequired("cron")
	_ = scheduleCreateCmd.MarkFlagRequired("description")

	return scheduleCreateCmd
}
