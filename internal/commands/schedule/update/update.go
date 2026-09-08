package update

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdUpdate(f cmdutils.Factory) *cobra.Command {
	scheduleUpdateCmd := &cobra.Command{
		Use:   "update <id> [flags]",
		Short: `Update a pipeline schedule.`,
		Long: heredoc.Docf(`
		Update a CI/CD pipeline schedule, identified by its numeric ID. Use
		the flags to change the cron expression, description, ref, time zone,
		or active state. Only the fields you specify are updated.

		To change pipeline variables, use %[1]s--create-variable%[1]s, %[1]s--update-variable%[1]s,
		and %[1]s--delete-variable%[1]s. The %[1]screate%[1]s and %[1]supdate%[1]s flags take
		%[1]skey:value%[1]s pairs; %[1]sdelete%[1]s takes a key. Pass each flag multiple times
		to change several variables.

		By default, the schedule is updated in the current project. Use
		%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Update the cron expression for a schedule
			glab schedule update 10 --cron "0 * * * *"

			# Update a schedule's description and ref
			glab schedule update 10 --description "Hourly build" --ref main

			# Add, change, and remove variables in one call
			glab schedule update 10 --create-variable "foo:bar" --update-variable "baz:qux" --delete-variable "old"
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			variablesToCreate, err := cmd.Flags().GetStringSlice("create-variable")
			if err != nil {
				return err
			}
			variablesToUpdate, err := cmd.Flags().GetStringSlice("update-variable")
			if err != nil {
				return err
			}
			variablesToDelete, err := cmd.Flags().GetStringSlice("delete-variable")
			if err != nil {
				return err
			}

			variablePairsToCreate := make([][2]string, 0, len(variablesToCreate))
			for _, v := range variablesToCreate {
				split := strings.SplitN(v, ":", 2)
				if len(split) != 2 {
					return fmt.Errorf("invalid format for --create-variable: %s", v)
				}

				variablePairsToCreate = append(variablePairsToCreate, [2]string{split[0], split[1]})
			}

			variablePairsToUpdate := make([][2]string, 0, len(variablesToUpdate))
			for _, v := range variablesToUpdate {
				split := strings.SplitN(v, ":", 2)
				if len(split) != 2 {
					return fmt.Errorf("invalid format for --update-variable: %s", v)
				}

				variablePairsToUpdate = append(variablePairsToUpdate, [2]string{split[0], split[1]})
			}

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return err
			}

			scheduleId := int64(id)

			opts := &gitlab.EditPipelineScheduleOptions{}

			description, _ := cmd.Flags().GetString("description")
			ref, _ := cmd.Flags().GetString("ref")
			cron, _ := cmd.Flags().GetString("cron")
			cronTimeZone, _ := cmd.Flags().GetString("cronTimeZone")
			active, _ := cmd.Flags().GetBool("active")

			if cmd.Flags().Lookup("active").Changed {
				opts.Active = &active
			}

			if description != "" {
				opts.Description = &description
			}

			if ref != "" {
				opts.Ref = &ref
			}

			if cron != "" {
				opts.Cron = &cron
			}

			if cronTimeZone != "" {
				opts.CronTimezone = &cronTimeZone
			}

			// skip API call if no changes are made
			if opts.Active != nil || opts.Description != nil || opts.Ref != nil || opts.Cron != nil || opts.CronTimezone != nil {
				_, _, err := client.PipelineSchedules.EditPipelineSchedule(repo.FullName(), scheduleId, opts)
				if err != nil {
					return err
				}
			}

			// create variables
			for _, v := range variablePairsToCreate {
				_, _, err := client.PipelineSchedules.CreatePipelineScheduleVariable(repo.FullName(), scheduleId, &gitlab.CreatePipelineScheduleVariableOptions{
					Key:   &v[0],
					Value: &v[1],
				})
				if err != nil {
					return err
				}
			}

			// update variables
			for _, v := range variablePairsToUpdate {
				_, _, err := client.PipelineSchedules.EditPipelineScheduleVariable(repo.FullName(), scheduleId, v[0], &gitlab.EditPipelineScheduleVariableOptions{
					Value: &v[1],
				})
				if err != nil {
					return err
				}
			}

			// delete variables
			for _, v := range variablesToDelete {
				_, _, err := client.PipelineSchedules.DeletePipelineScheduleVariable(repo.FullName(), scheduleId, v)
				if err != nil {
					return err
				}
			}

			f.IO().LogInfo("Updated schedule with ID", scheduleId)

			return nil
		},
	}

	scheduleUpdateCmd.Flags().String("description", "", "Description of the schedule.")
	scheduleUpdateCmd.Flags().String("ref", "", "Target branch or tag.")
	scheduleUpdateCmd.Flags().String("cron", "", "Cron interval pattern.")
	scheduleUpdateCmd.Flags().String("cronTimeZone", "", "Cron timezone.")
	scheduleUpdateCmd.Flags().Bool("active", true, "Whether or not the schedule is active.")
	scheduleUpdateCmd.Flags().StringSlice("create-variable", []string{}, "Pass new variables to schedule in format <key>:<value>.")
	scheduleUpdateCmd.Flags().StringSlice("update-variable", []string{}, "Pass updated variables to schedule in format <key>:<value>.")
	scheduleUpdateCmd.Flags().StringSlice("delete-variable", []string{}, "Pass variables you want to delete from schedule in format <key>.")
	scheduleUpdateCmd.Flags().Lookup("active").DefValue = "to not change"

	return scheduleUpdateCmd
}
