package create

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	catalog "gitlab.com/gitlab-org/cli/internal/commands/project/publish/catalog"
	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils"
	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils/upload"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type noteOptions int

const (
	noteOptMyOwn noteOptions = iota
	noteOptCommitLog
	noteOptGitTagMessage
	noteOptLeaveBlank
)

var noteOptionsNames = map[noteOptions]string{
	noteOptMyOwn:         "Write my own.",
	noteOptCommitLog:     "Write using the commit log as a template.",
	noteOptGitTagMessage: "Write using the Git tag message as the template.",
	noteOptLeaveBlank:    "Leave blank.",
}

type options struct {
	// The following fields must be exported for use with huh forms
	Name               string
	ReleaseNotesAction string

	ref                         string
	tagName                     string
	tagMessage                  string
	notes                       string
	notesFile                   string
	experimentalNotesTextOrFile string
	milestone                   []string
	assetLinksAsJSON            string
	releasedAt                  string
	repoOverride                string
	publishToCatalog            bool
	noUpdate                    bool
	noCloseMilestone            bool

	noteProvided bool

	assetLink  []*upload.ReleaseAsset
	assetFiles []*upload.ReleaseFile

	usePackageRegistry bool
	packageName        string

	ctx          context.Context
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	config       func() config.Config
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		config:       f.Config,
	}

	cmd := &cobra.Command{
		Use:   "create <tag> [<files>...]",
		Short: "Create a new GitLab release, or update an existing one.",
		Long: heredoc.Docf(`Create a new GitLab release for a repository, or
		update an existing one. Requires at least the Developer role.

		An existing release is updated with the new information you provide.

		To create a release from an annotated Git tag:

		1. Create the tag locally with Git, and push the tag to GitLab.
		2. Run this command.
		3. If the Git tag you specify doesn't exist, the command creates a
		   release from the latest state of the default branch, and tags it
		   with the tag name you specify.

		   To override this behavior, use %[1]s--ref%[1]s. The %[1]sref%[1]s
		   can be a commit SHA, another tag name, or a branch name.
		4. Optional. To fetch the new tag locally after the release, run
		   %[1]sgit fetch --tags origin%[1]s.

		Authentication in CI/CD:

		- Recommended: Use %[1]sCI_JOB_TOKEN%[1]s and turn on CI auto-login by
		  setting %[1]sGLAB_ENABLE_CI_AUTOLOGIN=true%[1]s. glab sends
		  %[1]sCI_JOB_TOKEN%[1]s in the %[1]sJOB-TOKEN%[1]s header, which the Releases
		  API accepts.
		- Alternative: Use %[1]sGITLAB_TOKEN%[1]s set to a project or group access token with the
		  %[1]sapi%[1]s scope.
		- Do not set %[1]sGITLAB_TOKEN=$CI_JOB_TOKEN%[1]s because the Releases API will
		  return a %[1]s404 Not Found%[1]s. %[1]sGITLAB_TOKEN%[1]s is sent as a personal access token in
		  the %[1]sPRIVATE-TOKEN%[1]s header, which the Releases API does not accept.

		The %[1]s--publish-to-catalog%[1]s flag is an experiment: it might be
		unstable or removed at any time, and is not ready for production use.
		For more information, see
		https://docs.gitlab.com/policy/development_stages_support/.
		`, "`"),
		Args: cmdutils.MinimumArgs(1, "no tag name provided."),
		Example: heredoc.Docf(`
			# Create a release interactively
			glab release create v1.0.1

			# Create a release non-interactively by specifying a note
			glab release create v1.0.1 --notes "bugfix release"

			# Use release notes from a file
			glab release create v1.0.1 -F changelog.md

			# Update an existing release (e.g., change the release date) without modifying notes
			glab release create v1.0.1 --released-at "2024-01-15T10:00:00Z"

			# Upload a release asset with a display name (type will default to 'other')
			glab release create v1.0.1 '/path/to/asset.zip#My display label'

			# Upload a release asset with a display name and type
			glab release create v1.0.1 '/path/to/asset.png#My display label#image'

			# Upload all assets in a specified folder (types default to 'other')
			glab release create v1.0.1 ./dist/*

			# Upload all tarballs in a specified folder (types default to 'other')
			glab release create v1.0.1 ./dist/*.tar.gz

			# Create a release from a CI/CD job using the job token
			GLAB_ENABLE_CI_AUTOLOGIN=true glab release create v1.0.1 --notes "See CHANGELOG.md" --ref "$CI_COMMIT_SHA"

			# Create a release from a CI/CD job using a project or group access token with the 'api' scope
			GITLAB_TOKEN="$ACCESS_TOKEN" glab release create v1.0.1 --notes "See CHANGELOG.md" --ref "$CI_COMMIT_SHA"

			# Create a release with assets specified as JSON object
			glab release create v1.0.1 --assets-links='
			[
			  {
			    "name": "Asset1",
			    "url":"https://<domain>/some/location/1",
			    "link_type": "other",
			    "direct_asset_path": "path/to/file"
			  }
			]'

			# (EXPERIMENTAL) Create a release and publish it to the GitLab CI/CD catalog
			# Requires the feature flag %[1]sci_release_cli_catalog_publish_option%[1]s to be enabled
			# for this project in your GitLab instance. Do NOT run this manually. Use it as part
			# of a CI/CD pipeline with the "release" keyword:
			#
			# - It retrieves components from the current repository by searching for
			#   %[1]syml%[1]s files within the "templates" directory and its subdirectories.
			# - It fails if the feature flag %[1]sci_release_cli_catalog_publish_option%[1]s
			#   is not enabled for this project in your GitLab instance.

			# Components can be defined:

			# - In single files ending in %[1]s.yml%[1]s for each component, like %[1]stemplates/secret-detection.yml%[1]s.
			# - In subdirectories containing %[1]stemplate.yml%[1]s files as entry points,
			#   for components that bundle together multiple related files. For example,
			#   %[1]stemplates/secret-detection/template.yml%[1]s.

			glab release create v1.0.1 --publish-to-catalog`, "`"),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ctx = cmd.Context()
			if err := opts.complete(cmd.Flags(), args); err != nil {
				return err
			}

			return createRun(opts)
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.Name, "name", "n", "", "The release name or title.")
	fl.StringVarP(&opts.ref, "ref", "r", "", "If the specified tag doesn't exist, create a release from the ref and tag it with the specified tag name. Accepts a commit SHA, tag name, or branch name.")
	fl.StringVarP(&opts.tagMessage, "tag-message", "T", "", "Message to use if creating a new annotated tag.")
	fl.StringVarP(&opts.notes, "notes", "N", "", "The release notes or description. Accepts Markdown.")
	fl.StringVarP(&opts.notesFile, "notes-file", "F", "", "Read release notes 'file'. To read from stdin, use '-'.")
	fl.StringVarP(&opts.releasedAt, "released-at", "D", "", "ISO 8601 datetime when the release was ready. Defaults to the current datetime.")
	fl.StringSliceVarP(&opts.milestone, "milestone", "m", []string{}, "The title of each milestone the release is associated with. Multiple milestones can be comma-separated or specified by repeating the flag.")
	fl.StringVarP(&opts.assetLinksAsJSON, "assets-links", "a", "", "JSON string representation of assets links. See documentation for example.")
	fl.BoolVar(&opts.publishToCatalog, "publish-to-catalog", false, "(EXPERIMENTAL) Publish the release to the GitLab CI/CD catalog.")
	fl.BoolVar(&opts.noUpdate, "no-update", false, "Prevent updating the existing release.")
	fl.BoolVar(&opts.noCloseMilestone, "no-close-milestone", false, "Prevent closing milestones after creating the release.")
	fl.StringVar(&opts.experimentalNotesTextOrFile, "experimental-notes-text-or-file", "", "(EXPERIMENTAL) Value to use as release notes. If a file exists with this value as path, its content will be used. Otherwise, the value itself will be used as text.")
	fl.BoolVar(&opts.usePackageRegistry, "use-package-registry", false, "Upload release assets to the generic package registry of the project. Overrides the GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY environment variable.")
	fl.StringVar(&opts.packageName, "package-name", upload.DefaultReleasePackageName, "The package name, when uploading assets to the generic package release with --use-package-registry.")
	cobra.CheckErr(fl.MarkHidden("experimental-notes-text-or-file"))

	// These two need to be separately exclusive to avoid a breaking change
	// because there may be existing scripts that already use both notes and notes-file.
	cmd.MarkFlagsMutuallyExclusive("experimental-notes-text-or-file", "notes")
	cmd.MarkFlagsMutuallyExclusive("experimental-notes-text-or-file", "notes-file")

	return cmd
}

func (o *options) complete(flags *pflag.FlagSet, args []string) error {
	o.repoOverride, _ = flags.GetString("repo")

	o.tagName = args[0]

	assetFiles, err := releaseutils.AssetsFromArgs(args[1:])
	if err != nil {
		return err
	}
	o.assetFiles = assetFiles

	if o.assetLinksAsJSON != "" {
		err := json.Unmarshal([]byte(o.assetLinksAsJSON), &o.assetLink)
		if err != nil {
			return fmt.Errorf("failed to parse JSON string: %w", err)
		}
	}

	o.notes, err = resolveNotes(flags, o)
	if err != nil {
		return err
	}
	o.noteProvided = o.notes != ""

	if !flags.Changed("use-package-registry") {
		if usePackageRegistry, err := strconv.ParseBool(os.Getenv("GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY")); err == nil {
			o.usePackageRegistry = usePackageRegistry
		}
	}

	return nil
}

func resolveNotes(flags *pflag.FlagSet, opts *options) (string, error) {
	if flags.Changed("notes") {
		return opts.notes, nil
	}

	if opts.notesFile != "" {
		return resolveNotesFile(opts)
	}

	if opts.experimentalNotesTextOrFile != "" {
		return resolveNotesFileOrText(opts)
	}

	return "", nil
}

func resolveNotesFile(opts *options) (string, error) {
	var b []byte
	var err error

	if opts.notesFile == "-" {
		b, err = io.ReadAll(opts.io.In)
		_ = opts.io.In.Close()
	} else {
		b, err = os.ReadFile(opts.notesFile)
	}

	if err != nil {
		return "", err
	}

	return string(b), nil
}

func resolveNotesFileOrText(opts *options) (string, error) {
	// Rules from: https://docs.gitlab.com/ci/yaml/#releasedescription

	// Rule 1: A file path can't have spaces
	if strings.Contains(strings.TrimSpace(opts.experimentalNotesTextOrFile), " ") {
		return opts.experimentalNotesTextOrFile, nil
	}

	// Rule 2: A file must be relative to the repository
	baseDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return "", err
	}

	f, err := root.Open(opts.experimentalNotesTextOrFile)
	if err != nil {
		// If the file simply doesn't exist, treat the value as text.
		// For any other error (e.g., permission denied), report it so
		// the user knows the file was found but couldn't be read.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
			return opts.experimentalNotesTextOrFile, nil
		}
		return "", fmt.Errorf("could not open file %q: %w", opts.experimentalNotesTextOrFile, err)
	}
	defer func() {
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
	}()

	b, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("could not read file %q: %w", opts.experimentalNotesTextOrFile, err)
	}

	return string(b), nil
}

func createRun(opts *options) error {
	client, err := opts.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := opts.baseRepo()
	if err != nil {
		return err
	}
	color := opts.io.Color()
	var tag *gitlab.Tag
	var resp *gitlab.Response

	if opts.ref == "" {
		opts.io.LogInfo(color.ProgressIcon(), "Validating tag", opts.tagName)
		tag, resp, err = client.Tags.GetTag(repo.FullName(), opts.tagName)
		if err != nil && resp != nil && resp.StatusCode != http.StatusNotFound {
			return cmdutils.WrapError(err, "could not fetch tag")
		}
		if tag == nil && resp != nil && resp.StatusCode == http.StatusNotFound {
			opts.io.LogInfo(color.DotWarnIcon(), "Tag does not exist.")
			opts.io.LogInfo(color.DotWarnIcon(), "No ref provided. Creating the tag from the latest state of the default branch.")
			project, err := repo.Project(client)
			if err != nil {
				// We are not able to retrieve the project from the API.
				// This is most likely because we are running in CI with a CI Job Token
				// which does not have access to the Projects API. Thus, let's check if we have access
				// to the predefined CI/CD variable CI_DEFAULT_BRANCH and use it instead if available.
				if defaultBranch, found := os.LookupEnv("CI_DEFAULT_BRANCH"); found {
					opts.io.LogInfof("%s using default branch %q as ref from CI_DEFAULT_BRANCH\n", color.ProgressIcon(), defaultBranch)
					opts.ref = defaultBranch
				}
			} else {
				opts.io.LogInfof("%s using default branch %q as ref\n", color.ProgressIcon(), project.DefaultBranch)
				opts.ref = project.DefaultBranch
			}
		}
	}

	if opts.io.PromptEnabled() && !opts.noteProvided {
		editorCommand, err := cmdutils.GetEditor(opts.config)
		if err != nil {
			return err
		}

		var tagDescription string
		var generatedChangelog string
		if tag == nil {
			tag, _, _ = client.Tags.GetTag(repo.FullName(), opts.tagName)
		}
		if tag != nil {
			tagDescription = tag.Message
		}
		if opts.repoOverride == "" {
			headRef := opts.tagName
			if tagDescription == "" {
				if opts.ref != "" {
					headRef = opts.ref
					brCfg := git.ReadBranchConfig(opts.ref)
					if brCfg.MergeRef != "" {
						headRef = brCfg.MergeRef
					}
				} else {
					headRef = "HEAD"
				}
			}

			if prevTag, err := detectPreviousTag(headRef); err == nil {
				commits, _ := changelogForRange(fmt.Sprintf("%s..%s", prevTag, headRef))
				generatedChangelog = generateChangelog(commits)
			}
		}

		editorOptions := []string{noteOptionsNames[noteOptMyOwn]}
		if generatedChangelog != "" {
			editorOptions = append(editorOptions, noteOptionsNames[noteOptCommitLog])
		}
		if tagDescription != "" {
			editorOptions = append(editorOptions, noteOptionsNames[noteOptGitTagMessage])
		}
		editorOptions = append(editorOptions, noteOptionsNames[noteOptLeaveBlank])

		// Combine title and release notes selection into a single form
		var fields []huh.Field

		// Add title input field
		fields = append(fields, huh.NewInput().
			Title("Release title (optional)").
			Value(&opts.Name))

		// Add release notes selection field
		fields = append(fields, huh.NewSelect[string]().
			Title("Release notes").
			Options(huh.NewOptions(editorOptions...)...).
			Value(&opts.ReleaseNotesAction))

		// Run the combined form
		err = opts.io.RunForm(opts.ctx, fields...)
		if err != nil {
			return fmt.Errorf("could not prompt: %w", err)
		}

		var openEditor bool
		var editorContents string

		switch opts.ReleaseNotesAction {
		case noteOptionsNames[noteOptMyOwn]:
			openEditor = true
		case noteOptionsNames[noteOptCommitLog]:
			openEditor = true
			editorContents = generatedChangelog
		case noteOptionsNames[noteOptGitTagMessage]:
			openEditor = true
			editorContents = tagDescription
		case noteOptionsNames[noteOptLeaveBlank]:
			openEditor = false
		default:
			return fmt.Errorf("invalid action: %v", opts.ReleaseNotesAction)
		}

		if openEditor {
			err = opts.io.Editor(opts.ctx, &opts.notes, "Release notes", "", editorContents, editorCommand)
			if err != nil {
				return err
			}
		}
	}
	start := time.Now()

	opts.io.LogInfof("%s Creating or updating release %s=%s %s=%s\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("tag"), opts.tagName)

	release, resp, err := client.Releases.GetRelease(repo.FullName(), opts.tagName)
	if err != nil && (resp == nil || (resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound)) {
		return releaseFailedErr(err, start)
	}

	var releasedAt time.Time

	if opts.releasedAt != "" {
		// Parse the releasedAt to the expected format of the API
		// From the API docs "Expected in ISO 8601 format (2019-03-15T08:00:00Z)".
		layout := time.RFC3339
		if len(opts.releasedAt) < len(layout) {
			layout = layout[:len(opts.releasedAt)]
		}
		releasedAt, err = time.Parse(layout, opts.releasedAt)
		if err != nil {
			return releaseFailedErr(err, start)
		}
	}

	if opts.Name == "" {
		opts.Name = opts.tagName
	}

	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound) || release == nil {
		createOpts := &gitlab.CreateReleaseOptions{
			Name:    &opts.Name,
			TagName: &opts.tagName,
		}

		if opts.notes != "" {
			createOpts.Description = &opts.notes
		}

		if opts.ref != "" {
			createOpts.Ref = &opts.ref
		}

		if opts.tagMessage != "" {
			createOpts.TagMessage = &opts.tagMessage
		}

		if opts.releasedAt != "" {
			createOpts.ReleasedAt = &releasedAt
		}

		if len(opts.milestone) > 0 {
			createOpts.Milestones = &opts.milestone
		}

		release, _, err = client.Releases.CreateRelease(repo.FullName(), createOpts)
		if err != nil {
			return releaseFailedErr(err, start)
		}
		opts.io.LogInfof("%s Release created:\t%s=%s\n", color.GreenCheck(),
			color.Blue("url"), release.Links.Self)
	} else {
		if opts.noUpdate {
			return releaseFailedErr(fmt.Errorf("release for tag %q already exists and --no-update flag was specified", opts.tagName), start)
		}

		updateOpts := &gitlab.UpdateReleaseOptions{
			Name: &opts.Name,
		}
		if opts.notes != "" {
			updateOpts.Description = &opts.notes
		}

		if opts.releasedAt != "" {
			updateOpts.ReleasedAt = &releasedAt
		}

		if len(opts.milestone) > 0 {
			updateOpts.Milestones = &opts.milestone
		}

		release, _, err = client.Releases.UpdateRelease(repo.FullName(), opts.tagName, updateOpts)
		if err != nil {
			return releaseFailedErr(err, start)
		}

		opts.io.LogInfof("%s Release updated\t%s=%s\n", color.GreenCheck(),
			color.Blue("url"), release.Links.Self)
	}

	// upload files and create asset links
	err = releaseutils.CreateReleaseAssets(opts.io, client, opts.assetFiles, opts.assetLink, repo.FullName(), release.TagName, opts.packageName, opts.usePackageRegistry)
	if err != nil {
		return releaseFailedErr(err, start)
	}

	if opts.noCloseMilestone {
		opts.io.LogInfof("%s Skipping closing milestones\n", color.GreenCheck())
	} else {
		if len(opts.milestone) > 0 {
			// close all associated milestones
			for _, milestone := range opts.milestone {
				opts.io.StartSpinner("Closing milestone %q", milestone)
				err := closeMilestone(opts, milestone)
				opts.io.StopSpinner("")
				if err != nil {
					opts.io.LogError(color.FailedIcon(), err.Error())
				} else {
					opts.io.LogInfof("%s Closed milestone %q\n", color.GreenCheck(), milestone)
				}
			}
		}
	}
	opts.io.LogInfof(color.Bold("%s Release succeeded after %0.2f seconds.\n"), color.GreenCheck(), time.Since(start).Seconds())

	if opts.publishToCatalog {
		err = catalog.Publish(opts.io, client, repo.FullName(), release.TagName)
		if err != nil {
			return cmdutils.WrapError(err, "failed to publish the release to the GitLab CI/CD catalog.")
		}
	}

	return nil
}

func releaseFailedErr(err error, start time.Time) error {
	return cmdutils.WrapError(err, fmt.Sprintf("release failed after %0.2f seconds.", time.Since(start).Seconds()))
}

func getMilestoneByTitle(c *options, title string) (*gitlab.Milestone, error) {
	opts := &gitlab.ListMilestonesOptions{
		Title: &title,
	}

	client, err := c.gitlabClient()
	if err != nil {
		return nil, err
	}

	repo, err := c.baseRepo()
	if err != nil {
		return nil, err
	}

	for {
		milestones, resp, err := client.Milestones.ListMilestones(repo.FullName(), opts)
		if err != nil {
			return nil, err
		}

		for _, milestone := range milestones {
			if milestone != nil && milestone.Title == title {
				return milestone, nil
			}
		}

		if resp.NextPage == 0 {
			break
		}

		opts.Page = resp.NextPage
	}

	return nil, nil
}

// CloseMilestone closes a given milestone.
func closeMilestone(c *options, title string) error {
	client, err := c.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := c.baseRepo()
	if err != nil {
		return err
	}

	milestone, err := getMilestoneByTitle(c, title)
	if err != nil {
		return err
	}

	if milestone == nil {
		return fmt.Errorf("could not find milestone: %q", title)
	}

	closeStateEvent := "close"

	opts := &gitlab.UpdateMilestoneOptions{
		Description: &milestone.Description,
		DueDate:     milestone.DueDate,
		StartDate:   milestone.StartDate,
		StateEvent:  &closeStateEvent,
		Title:       &milestone.Title,
	}

	_, _, err = client.Milestones.UpdateMilestone(
		repo.FullName(),
		milestone.ID,
		opts,
	)

	return err
}

func detectPreviousTag(headRef string) (string, error) {
	cmd := git.GitCommand("describe", "--tags", "--abbrev=0", fmt.Sprintf("%s^", headRef))
	b, err := run.PrepareCmd(cmd).Output()
	return strings.TrimSpace(string(b)), err
}

type logEntry struct {
	Subject string
	Body    string
}

func changelogForRange(refRange string) ([]logEntry, error) {
	cmd := git.GitCommand("-c", "log.ShowSignature=false", "log", "--first-parent", "--reverse", "--pretty=format:%B%x00", refRange)

	b, err := run.PrepareCmd(cmd).Output()
	if err != nil {
		return nil, err
	}

	var entries []logEntry
	for cb := range bytes.SplitSeq(b, []byte{'\000'}) {
		c := strings.ReplaceAll(string(cb), "\r\n", "\n")
		c = strings.TrimPrefix(c, "\n")
		if c == "" {
			continue
		}
		parts := strings.SplitN(c, "\n\n", 2)
		var body string
		subject := strings.ReplaceAll(parts[0], "\n", " ")
		if len(parts) > 1 {
			body = parts[1]
		}
		entries = append(entries, logEntry{
			Subject: subject,
			Body:    body,
		})
	}

	return entries, nil
}

func generateChangelog(commits []logEntry) string {
	var parts []string
	for _, c := range commits {
		parts = append(parts, fmt.Sprintf("* %s", c.Subject))
		if c.Body != "" {
			parts = append(parts, utils.Indent(c.Body, "  "))
		}
	}
	return strings.Join(parts, "\n\n")
}
