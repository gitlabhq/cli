package view

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/gdamore/tcell/v2"
	"github.com/lunixbochs/vtclean"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	config       func() config.Config

	refName       string
	openInBrowser bool
	pipelineID    int64
}

type ViewJobKind int64

const (
	Job ViewJobKind = iota
	Bridge
)

type ViewJob struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	ErasedAt     *time.Time `json:"erased_at"`
	Duration     float64    `json:"duration"`
	Stage        string     `json:"stage"`
	Status       string     `json:"status"`
	AllowFailure bool       `json:"allow_failure"`

	Kind ViewJobKind

	OriginalJob    *gitlab.Job
	OriginalBridge *gitlab.Bridge
}

func ViewJobFromBridge(bridge *gitlab.Bridge) *ViewJob {
	vj := &ViewJob{}
	vj.ID = bridge.ID
	vj.Name = bridge.Name
	vj.Status = bridge.Status
	vj.Stage = bridge.Stage
	vj.StartedAt = bridge.StartedAt
	vj.FinishedAt = bridge.FinishedAt
	vj.ErasedAt = bridge.ErasedAt
	vj.Duration = bridge.Duration
	vj.AllowFailure = bridge.AllowFailure
	vj.OriginalBridge = bridge
	vj.Kind = Bridge
	return vj
}

func ViewJobFromJob(job *gitlab.Job) *ViewJob {
	vj := &ViewJob{}
	vj.ID = job.ID
	vj.Name = job.Name
	vj.Status = job.Status
	vj.Stage = job.Stage
	vj.StartedAt = job.StartedAt
	vj.FinishedAt = job.FinishedAt
	vj.ErasedAt = job.ErasedAt
	vj.Duration = job.Duration
	vj.AllowFailure = job.AllowFailure
	vj.OriginalJob = job
	vj.Kind = Job
	return vj
}

func NewCmdView(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		config:       f.Config,
	}
	pipelineCIView := &cobra.Command{
		Use:   "view [<branch | tag>]",
		Short: "View, run, retry, and cancel CI/CD pipeline jobs.",
		Long: heredoc.Docf(`Supports viewing, running, tracing, and canceling jobs.

		Use arrow keys to navigate jobs and logs.

		- %[1]sEnter%[1]s to toggle through a job's logs / traces, or display a child pipeline.
		  Trigger jobs are marked with a %[1]s»%[1]s.
		- %[1]sEsc%[1]s or %[1]sq%[1]s to close the logs or trace, or return to the parent pipeline.
		- %[1]sCtrl+R%[1]s, %[1]sCtrl+P%[1]s to run, retry, or play a job. Use %[1]sTab%[1]s or arrow keys to
		  navigate the modal, and %[1]sEnter%[1]s to confirm.
		- %[1]sCtrl+D%[1]s to cancel a job. If the selected job isn't running or pending,
		  quits the CI/CD view.
		- %[1]sCtrl+Q%[1]s to quit the CI/CD view.
		- %[1]sCtrl+Space%[1]s to suspend application and view the logs. Similar to %[1]sglab ci trace%[1]s.
		- Supports %[1]svi%[1]s style bindings and arrow keys for navigating jobs and logs.
	`, "`"),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
			mcpannotations.Interactive: "true",
		},
		Example: heredoc.Doc(`
			# Use the current branch
			glab ci view

			# View the latest pipeline on main
			glab ci view main

			# View the latest pipeline on main using a flag
			glab ci view -b main

			# View the latest pipeline on main for another project
			glab ci view -b main -R myusername/myproject`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	pipelineCIView.Flags().
		StringVarP(&opts.refName, "branch", "b", "", "Check pipeline status for a branch or tag. Defaults to the current branch.")
	pipelineCIView.Flags().BoolVarP(&opts.openInBrowser, "web", "w", false, "Open pipeline in a browser. Uses the default browser, or the browser specified in the BROWSER environment variable.")
	pipelineCIView.Flags().Int64VarP(&opts.pipelineID, "pipelineid", "p", 0, "Check pipeline status for a specific pipeline ID.")
	pipelineCIView.MarkFlagsMutuallyExclusive("branch", "pipelineid")

	return pipelineCIView
}

func (o *options) complete(args []string) error {
	if o.refName == "" {
		if len(args) == 1 {
			o.refName = args[0]
		} else {
			refName, err := git.CurrentBranch()
			if err != nil {
				return err
			}
			o.refName = refName
		}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	// Check if we're in an interactive terminal (TTY required for TUI)
	if !o.io.IsOutputTTY() {
		return fmt.Errorf("ci view requires an interactive terminal (TTY).\nFor non-interactive use, try:\n  - 'glab ci status' to check pipeline status\n  - 'glab ci get' to view pipeline details\n  - 'glab ci trace' to view job logs")
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	projectID := repo.FullName()
	var pipelineID int64
	var webURL string
	var pipelineCreatedAt time.Time
	var commit *gitlab.Commit
	var commitSHA string

	if o.pipelineID != 0 {
		pipeline, _, err := client.Pipelines.GetPipeline(projectID, o.pipelineID)
		if err != nil {
			return err
		}

		pipelineID = pipeline.ID
		webURL = pipeline.WebURL
		pipelineCreatedAt = *pipeline.CreatedAt
		commitSHA = pipeline.SHA
		commit, _, err = client.Commits.GetCommit(projectID, commitSHA, nil)
		if err != nil {
			return err
		}
	} else {
		// Get pipeline by branch reference (not by commit's LastPipeline)
		pipeline, err := ciutils.GetPipelineWithFallback(ctx, client, projectID, o.refName, o.io)
		if err != nil {
			return err
		}

		pipelineID = pipeline.ID
		webURL = pipeline.WebURL
		pipelineCreatedAt = *pipeline.CreatedAt
		commitSHA = pipeline.SHA

		// Get commit details for display purposes
		commit, _, err = client.Commits.GetCommit(projectID, commitSHA, nil)
		if err != nil {
			return err
		}
	}

	if o.openInBrowser { // open in browser if --web flag is specified
		if o.io.IsOutputTTY() {
			o.io.LogErrorf("Opening %s in your browser.\n", utils.DisplayURL(webURL))
		}

		cfg := o.config()
		browser, _ := cfg.Get(repo.RepoHost(), "browser")
		return utils.OpenInBrowser(webURL, browser)
	}

	p, _, err := client.Pipelines.GetPipeline(projectID, pipelineID)
	if err != nil {
		return fmt.Errorf("can't get pipeline #%d info: %w", pipelineID, err)
	}
	pipelineUser := p.User

	pipelines = make([]gitlab.PipelineInfo, 0, 10)

	// Use terminal default colors instead of tview's hardcoded white-on-black theme.
	tview.Styles = tview.Theme{}

	root := tview.NewPages()
	root.
		SetBackgroundColor(tcell.ColorDefault).
		SetBorderPadding(1, 1, 2, 2).
		SetBorder(true).
		SetTitle(fmt.Sprintf(" Pipeline #%d triggered %s by %s ", pipelineID, utils.TimeToPrettyTimeAgo(pipelineCreatedAt), pipelineUser.Name))

	boxes = make(map[string]*tview.TextView)
	jobsCh := make(chan []*ViewJob)
	forceUpdateCh := make(chan bool)
	inputCh := make(chan struct{})

	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	defer func() {
		if screen != nil {
			screen.Fini()
		}
	}()
	app := tview.NewApplication()
	defer recoverPanic(app)

	var navi navigator
	app.SetInputCapture(inputCapture(ctx, app, root, &navi, inputCh, forceUpdateCh, o, client, projectID, commitSHA))
	go updateJobs(app, jobsCh, forceUpdateCh, client, commit)
	go func() {
		defer recoverPanic(app)
		for {
			app.SetFocus(root)
			jobsView(ctx, app, jobsCh, inputCh, root, client, projectID, commitSHA)
			app.Draw()
		}
	}()
	if err := app.SetScreen(screen).SetRoot(root, true).SetAfterDrawFunc(linkJobsView(app)).Run(); err != nil {
		return err
	}
	return nil
}

// handleBridgeJobSelection handles the user pressing Enter on a bridge job (downstream pipeline trigger).
// It navigates to the downstream pipeline if it exists, or shows an informational modal if it doesn't.
func handleBridgeJobSelection(app *tview.Application, root *tview.Pages, forceUpdateCh chan<- bool, navi *navigator) {
	// If downstream pipeline exists, navigate to it
	if curJob.OriginalBridge.DownstreamPipeline != nil {
		pipelines = append(pipelines, *curJob.OriginalBridge.DownstreamPipeline)
		curJob = nil
		// Reset cursor: the child pipeline has a different jobs slice, so a stale
		// idx/depth from the parent can index past the end and crash Navigate (#8313).
		*navi = navigator{}
		forceUpdateCh <- true
		app.ForceDraw()
		return
	}

	// No downstream pipeline exists yet - show informational modal
	modalVisible = true

	// Determine appropriate message based on job status
	// Note: We suggest GitLab UI instead of Ctrl+R/P because bridge jobs cannot be
	// manually triggered via the CLI - they are excluded from the run/retry commands
	// (see line 377: if curJob.Kind != Job { break })
	var message string
	switch curJob.Status {
	case string(gitlab.Manual):
		message = fmt.Sprintf("Downstream pipeline '%s' is waiting to be triggered.\n\nStatus: %s\n\nThis trigger will create a child pipeline when it runs.\nTrigger it via the GitLab UI or wait for the pipeline to run it.", curJob.Name, curJob.Status)
	case string(gitlab.Pending), string(gitlab.Created):
		message = fmt.Sprintf("Downstream pipeline '%s' has not started yet.\n\nStatus: %s\n\nThis trigger will create a child pipeline when it runs.", curJob.Name, curJob.Status)
	case string(gitlab.Skipped):
		message = fmt.Sprintf("Downstream pipeline '%s' was skipped and won't create a child pipeline.\n\nStatus: %s", curJob.Name, curJob.Status)
	default:
		message = fmt.Sprintf("Downstream pipeline '%s' has not triggered a child pipeline yet.\n\nStatus: %s\n\nThe child pipeline will appear here once the trigger completes.", curJob.Name, curJob.Status)
	}

	modal := tview.NewModal().
		SetBackgroundColor(tcell.ColorDefault).
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			modalVisible = false
			root.RemovePage("downstream-info")
			app.ForceDraw()
		})
	root.AddAndSwitchToPage("downstream-info", modal, false)
	app.ForceDraw()
}

func inputCapture(
	ctx context.Context,
	app *tview.Application,
	root *tview.Pages,
	navi *navigator,
	inputCh chan<- struct{},
	forceUpdateCh chan<- bool,
	opts *options,
	apiClient *gitlab.Client,
	projectID string,
	commitSHA string,
) func(event *tcell.EventKey) *tcell.EventKey {
	return func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' || event.Key() == tcell.KeyEscape {
			switch {
			case modalVisible:
				modalVisible = !modalVisible
				root.HidePage("yesno")
				if inputCh == nil {
					inputCh <- struct{}{}
				}
			case logsVisible:
				logsVisible = !logsVisible
				root.HidePage("logs-" + curJob.Name)
				if inputCh == nil {
					inputCh <- struct{}{}
				}
				app.ForceDraw()
			case len(pipelines) > 0:
				pipelines = pipelines[:len(pipelines)-1]
				curJob = nil
				// Reset cursor: see comment in handleBridgeJobSelection (#8313).
				*navi = navigator{}
				forceUpdateCh <- true
				app.ForceDraw()
			default:
				app.Stop()
				return nil
			}
		}
		if !modalVisible && !logsVisible && len(jobs) > 0 {
			curJob = navi.Navigate(jobs, event)
			root.SendToFront("jobs-" + curJob.Name)
			if inputCh == nil {
				inputCh <- struct{}{}
			}
		}
		event = normalizeCtrlM(event)
		switch event.Key() {
		case tcell.KeyCtrlQ:
			app.Stop()
			return nil
		case tcell.KeyCtrlD:
			if curJob.Kind == Job && (curJob.Status == string(gitlab.Pending) || curJob.Status == string(gitlab.Running)) {
				modalVisible = true
				modal := tview.NewModal().
					SetBackgroundColor(tcell.ColorDefault).
					SetText(fmt.Sprintf("Are you sure you want to cancel %s?", curJob.Name)).
					AddButtons([]string{"✘ No", "✔ Yes"}).
					SetDoneFunc(func(buttonIndex int, buttonLabel string) {
						modalVisible = false
						root.RemovePage("yesno")
						if buttonLabel == "✘ No" {
							app.ForceDraw()
							return
						}
						root.RemovePage("logs-" + curJob.Name)
						app.ForceDraw()
						job, _, err := apiClient.Jobs.CancelJob(projectID, curJob.ID)
						if err != nil {
							app.Stop()
							log.Fatal(err)
						}
						if job != nil {
							curJob = ViewJobFromJob(job)
							app.ForceDraw()
						}
					})
				root.AddAndSwitchToPage("yesno", modal, false)
				inputCh <- struct{}{}
				app.ForceDraw()
				return nil
			}
		case tcell.KeyCtrlP, tcell.KeyCtrlR:
			if modalVisible || curJob.Kind != Job {
				break
			}
			modalVisible = true
			modal := tview.NewModal().
				SetBackgroundColor(tcell.ColorDefault).
				SetText(fmt.Sprintf("Are you sure you want to run %s?", curJob.Name)).
				AddButtons([]string{"✘ No", "✔ Yes"}).
				SetDoneFunc(func(buttonIndex int, buttonLabel string) {
					modalVisible = false
					root.RemovePage("yesno")
					if buttonLabel != "✔ Yes" {
						app.ForceDraw()
						return
					}
					root.RemovePage("logs-" + curJob.Name)
					app.ForceDraw()

					job, err := api.PlayOrRetryJobs(
						apiClient,
						projectID,
						curJob.ID,
						curJob.Status,
					)
					if err != nil {
						app.Stop()
						log.Fatal(err)
					}
					if job != nil {
						curJob = ViewJobFromJob(job)
						app.ForceDraw()
					}
				})
			root.AddAndSwitchToPage("yesno", modal, false)
			inputCh <- struct{}{}
			app.ForceDraw()
			return nil
		case tcell.KeyEnter:
			if !modalVisible {
				if curJob.Kind == Job {
					logsVisible = !logsVisible
					if !logsVisible {
						root.HidePage("logs-" + curJob.Name)
					}
					inputCh <- struct{}{}
					app.ForceDraw()
				} else {
					// Downstream pipeline trigger selected
					handleBridgeJobSelection(app, root, forceUpdateCh, navi)
				}
				return nil
			}
		case tcell.KeyCtrlSpace:
			app.Suspend(func() {
				ctx, cancel := context.WithCancel(ctx)
				go func() {
					err := ciutils.RunTraceSha(
						ctx,
						apiClient,
						opts.io.StdOut,
						projectID,
						commitSHA,
						curJob.Name,
					)
					if err != nil {
						app.Stop()
						log.Fatal(err)
					}
					if ctx.Err() == nil {
						fmt.Println("\nPress <Enter> to resume the ci GUI view.")
					}
				}()
				reader := bufio.NewReader(os.Stdin)
				for {
					r, _, err := reader.ReadRune()
					if !errors.Is(err, io.EOF) && err != nil {
						app.Stop()
						log.Fatal(err)
					}
					if r == '\n' {
						cancel()
						break
					}
				}
			})
			if inputCh == nil {
				inputCh <- struct{}{}
			}
			return nil
		}
		if inputCh == nil {
			inputCh <- struct{}{}
		}
		return event
	}
}

func normalizeCtrlM(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlM {
		return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	}
	return event
}

var (
	logsVisible, modalVisible bool
	curJob                    *ViewJob
	jobs                      []*ViewJob
	pipelines                 []gitlab.PipelineInfo
	boxes                     map[string]*tview.TextView
)

// bracketEscaper wraps a writer and escapes square brackets for tview, but preserves ANSI escape sequences.
// This is necessary because tview interprets square brackets as color tag markers.
// For example, [MASKED] would be treated as a color tag and stripped from display.
// By escaping closing brackets to [], we prevent tview from parsing literal brackets as tags.
type bracketEscaper struct {
	io.Writer
}

func (b *bracketEscaper) Write(p []byte) (int, error) {
	// Build escaped output, preserving ANSI escape sequences
	// In tview's escaping convention, only closing ] needs to be escaped to []
	var result strings.Builder
	i := 0
	for i < len(p) {
		// Check if this is the start of an ANSI escape sequence: ESC [
		if i < len(p)-1 && p[i] == '\x1b' && p[i+1] == '[' {
			// Find the end of the ANSI sequence (ends with a letter)
			result.WriteByte(p[i])   // ESC
			result.WriteByte(p[i+1]) // [
			i += 2
			// Copy the rest of the ANSI sequence
			for i < len(p) && !((p[i] >= 'A' && p[i] <= 'Z') || (p[i] >= 'a' && p[i] <= 'z')) {
				result.WriteByte(p[i])
				i++
			}
			if i < len(p) {
				result.WriteByte(p[i]) // Final letter
				i++
			}
		} else if p[i] == ']' {
			// Literal closing bracket - escape it for tview by replacing with []
			result.WriteString("[]")
			i++
		} else {
			result.WriteByte(p[i])
			i++
		}
	}

	// Write the escaped data to the underlying writer
	_, err := b.Writer.Write([]byte(result.String()))
	if err != nil {
		return 0, err
	}
	// Return the number of bytes consumed from input (per io.Writer contract)
	// We successfully processed all input bytes even though output may be longer
	return len(p), nil
}

func curPipeline(commit *gitlab.Commit) (gitlab.PipelineInfo, error) {
	if len(pipelines) > 0 {
		return pipelines[len(pipelines)-1], nil
	}
	if commit.LastPipeline == nil {
		return gitlab.PipelineInfo{}, fmt.Errorf("commit %s has no associated pipeline", commit.ID)
	}
	return *commit.LastPipeline, nil
}

// navigator manages the internal state for processing tcell.EventKeys
type navigator struct {
	depth, idx int
}

// Navigate uses the ci stages as boundaries and returns the currently focused
// job index after processing a *tcell.EventKey
func (n *navigator) Navigate(jobs []*ViewJob, event *tcell.EventKey) *ViewJob {
	// Defensive clamp: callers reset navi when switching pipelines, but a stale
	// idx left over from a larger jobs slice would otherwise panic here (#8313).
	if n.idx >= len(jobs) {
		n.idx = 0
		n.depth = 0
	}
	stage := jobs[n.idx].Stage
	prev, next := adjacentStages(jobs, stage)
	switch event.Key() {
	case tcell.KeyLeft:
		stage = prev
	case tcell.KeyRight:
		stage = next
	}
	switch event.Rune() {
	case 'h':
		stage = prev
	case 'l':
		stage = next
	}
	l, u := stageBounds(jobs, stage)

	switch event.Key() {
	case tcell.KeyDown:
		n.depth++
		if n.depth > u-l {
			n.depth = u - l
		}
	case tcell.KeyUp:
		n.depth--
	}
	switch event.Rune() {
	case 'j':
		n.depth++
		if n.depth > u-l {
			n.depth = u - l
		}
	case 'k':
		n.depth--
	case 'g':
		n.depth = 0
	case 'G':
		n.depth = u - l
	}

	if n.depth < 0 {
		n.depth = 0
	}
	n.idx = min(l+n.depth, u)
	return jobs[n.idx]
}

func stageBounds(jobs []*ViewJob, s string) (int, int) {
	if len(jobs) <= 1 {
		return 0, 0
	}
	var l, u int
	p := jobs[0].Stage
	for i, v := range jobs {
		if v.Stage != s && u != 0 {
			return l, u
		}
		if v.Stage != p {
			l = i
			p = v.Stage
		}
		if v.Stage == s {
			u = i
		}
	}
	return l, u
}

func adjacentStages(jobs []*ViewJob, s string) (string, string) {
	if len(jobs) == 0 {
		return "", ""
	}
	p := jobs[0].Stage

	var n string
	for _, v := range jobs {
		if v.Stage != s && n != "" {
			n = v.Stage
			return p, n
		}
		if v.Stage == s {
			n = "cur"
		}
		if n == "" {
			p = v.Stage
		}
	}
	n = jobs[len(jobs)-1].Stage
	return p, n
}

func jobsView(
	ctx context.Context,
	app *tview.Application,
	jobsCh <-chan []*ViewJob,
	inputCh <-chan struct{},
	root *tview.Pages,
	apiClient *gitlab.Client,
	projectID string,
	commitSHA string,
) {
	select {
	case jobs = <-jobsCh:
	case <-inputCh:
	case <-time.NewTicker(time.Second * 1).C:
	}
	if jobs == nil {
		jobs = <-jobsCh
	}
	if curJob == nil && len(jobs) > 0 {
		curJob = jobs[0]
	}
	if modalVisible {
		return
	}
	if logsVisible {
		logsKey := "logs-" + curJob.Name
		if !root.SwitchToPage(logsKey).HasPage(logsKey) {
			tv := tview.NewTextView()
			tv.
				SetDynamicColors(true).
				SetBackgroundColor(tcell.ColorDefault).
				SetBorderPadding(0, 0, 1, 1).
				SetBorder(true)

			go func() {
				// Chain: bracketEscaper -> vtclean -> ANSIWriter -> TextView
				//
				// The bracketEscaper must come FIRST in the chain to escape literal square
				// brackets (like [MASKED]) before they reach tview. This prevents tview from
				// interpreting them as color tags and removing them.
				//
				// Flow:
				// 1. Raw trace with ANSI codes and [MASKED] text
				// 2. bracketEscaper: Escapes ] to [] while preserving ANSI codes
				// 3. vtclean: Cleans terminal control sequences, preserves ANSI colors
				// 4. ANSIWriter: Converts ANSI codes to tview color tags
				// 5. TextView: Displays with colors and escaped brackets
				ansiWriter := tview.ANSIWriter(tv)
				vtcleanWriter := vtclean.NewWriter(ansiWriter, true)
				bracketWriter := &bracketEscaper{Writer: vtcleanWriter}

				err := ciutils.RunTraceSha(
					ctx,
					apiClient,
					bracketWriter,
					projectID,
					commitSHA,
					curJob.Name,
				)
				if err != nil {
					app.Stop()
					log.Fatal(err)
				}
			}()
			root.AddAndSwitchToPage("logs-"+curJob.Name, tv, true)
		}
		return
	}
	px, _, maxX, maxY := root.GetInnerRect()
	var (
		stages    = 0
		lastStage = ""
	)
	// get the number of stages
	for _, j := range jobs {
		if j.Stage != lastStage {
			lastStage = j.Stage
			stages++
		}
	}
	lastStage = ""
	var (
		rowIdx   int
		stageIdx int
		maxTitle = 20
	)
	boxKeys := make(map[string]bool)
	for _, j := range jobs {
		boxX := px + (maxX / stages * stageIdx)
		if j.Stage != lastStage {
			stageIdx++
			lastStage = j.Stage
			key := "stage-" + j.Stage
			boxKeys[key] = true

			x, y, w, h := boxX, maxY/6-4, maxTitle+2, 3
			b := box(root, key, x, y, w, h)

			caser := cases.Title(language.English)
			b.SetText(caser.String(j.Stage))
			b.SetTextAlign(tview.AlignCenter)
		}
	}
	lastStage = jobs[0].Stage
	rowIdx = 0
	stageIdx = 0
	for _, j := range jobs {
		if j.Stage != lastStage {
			rowIdx = 0
			lastStage = j.Stage
			stageIdx++
		}
		boxX := px + (maxX / stages * stageIdx)

		key := "jobs-" + j.Name
		boxKeys[key] = true
		x, y, w, h := boxX, maxY/6+(rowIdx*5), maxTitle+2, 4
		b := box(root, key, x, y, w, h)
		b.SetTitle(j.Name)
		// The scope of jobs to show, one or array of: created, pending, running,
		// failed, success, canceled, skipped; showing all jobs if none provided
		var statChar rune
		switch j.Status {
		case string(gitlab.Success):
			b.SetBorderColor(tcell.ColorGreen)
			statChar = '✔'
		case string(gitlab.Failed):
			if j.AllowFailure {
				b.SetBorderColor(tcell.ColorOrange)
				statChar = '!'
			} else {
				b.SetBorderColor(tcell.ColorRed)
				statChar = '✘'
			}
		case string(gitlab.Running):
			b.SetBorderColor(tcell.ColorBlue)
			statChar = '●'
		case string(gitlab.Pending):
			b.SetBorderColor(tcell.ColorYellow)
			statChar = '●'
		case string(gitlab.Manual):
			b.SetBorderColor(tcell.ColorGrey)
			statChar = '■'
		case string(gitlab.Canceled):
			statChar = 'Ø'
		case string(gitlab.Skipped):
			statChar = '»'
		}
		// retryChar := '⟳'
		title := fmt.Sprintf("%c %s", statChar, j.Name)
		// trim the suffix if it matches the stage, I've seen
		// the pattern in 2 different places to handle
		// different stages for the same service and it tends
		// to make the title spill over the max
		title = strings.TrimSuffix(title, ":"+j.Stage)
		b.SetTitle(title)
		// tview default aligns center, which is nice, but if
		// the title is too long we want to bias towards seeing
		// the beginning of it
		if tview.TaggedStringWidth(title) > maxTitle {
			b.SetTitleAlign(tview.AlignLeft)
		}
		triggerText := ""
		if j.Kind == Bridge {
			triggerText = "»"
		}
		if j.StartedAt != nil {
			end := time.Now()
			if j.FinishedAt != nil {
				end = *j.FinishedAt
			}
			b.SetText(triggerText + "\n" + utils.FmtDuration(end.Sub(*j.StartedAt)))
			b.SetTextAlign(tview.AlignRight)
		} else {
			b.SetText(triggerText)
		}
		b.SetTextAlign(tview.AlignRight)
		rowIdx++

	}
	for k := range boxes {
		if !boxKeys[k] {
			root.RemovePage(k)
		}
	}
	root.SendToFront("jobs-" + curJob.Name)
}

func box(root *tview.Pages, key string, x, y, w, h int) *tview.TextView {
	b, ok := boxes[key]
	if !ok {
		b = tview.NewTextView()
		b.
			SetBackgroundColor(tcell.ColorDefault).
			SetBorder(true)
		boxes[key] = b
	}
	b.SetRect(x, y, w, h)

	root.AddPage(key, b, false, true)
	return b
}

func recoverPanic(app *tview.Application) {
	if r := recover(); r != nil {
		app.Stop()
		log.Fatalf("%s\n%s\n", r, string(debug.Stack()))
	}
}

func updateJobs(
	app *tview.Application,
	jobsCh chan<- []*ViewJob,
	forceUpdateCh <-chan bool,
	apiClient *gitlab.Client,
	commit *gitlab.Commit,
) {
	defer recoverPanic(app)
	for {
		if modalVisible {
			time.Sleep(time.Second * 1)
			continue
		}
		var jobs []*gitlab.Job
		var bridges []*gitlab.Bridge
		pipeline, err := curPipeline(commit)
		if err != nil {
			app.Stop()
			log.Fatalf("%v", err)
		}
		jobs, bridges, err = api.PipelineJobsWithID(
			apiClient,
			pipeline.ProjectID,
			pipeline.ID,
		)
		if err != nil {
			app.Stop()
			log.Fatalf("failed to find CI jobs: %v", err)
		}
		if len(jobs) == 0 && len(bridges) == 0 {
			app.Stop()
			log.Fatal("No jobs found in the pipeline. Your '.gitlab-ci.yml' file might be invalid, or the pipeline triggered no jobs.")
		}
		viewJobs := make([]*ViewJob, 0, len(jobs)+len(bridges))
		for _, j := range jobs {
			viewJobs = append(viewJobs, ViewJobFromJob(j))
		}
		for _, b := range bridges {
			viewJobs = append(viewJobs, ViewJobFromBridge(b))
		}
		jobsCh <- latestJobs(viewJobs)
		select {
		case <-forceUpdateCh:
		case <-time.After(time.Second * 5):
		}

	}
}

func linkJobsView(app *tview.Application) func(screen tcell.Screen) {
	return func(screen tcell.Screen) {
		defer recoverPanic(app)
		err := linkJobs(screen, jobs, boxes)
		if err != nil {
			app.Stop()
			log.Fatal(err)
		}
	}
}

func linkJobs(screen tcell.Screen, jobs []*ViewJob, boxes map[string]*tview.TextView) error {
	if logsVisible || modalVisible {
		return nil
	}
	for i, j := range jobs {
		if _, ok := boxes["jobs-"+j.Name]; !ok {
			return fmt.Errorf("jobs-%s not found at index: %d", jobs[i].Name, i)
		}
	}
	var padding int
	// find the amount of space between two jobs is adjacent stages
	for i, k := 0, 1; k < len(jobs); i, k = i+1, k+1 {
		if jobs[i].Stage == jobs[k].Stage {
			continue
		}
		x1, _, w, _ := boxes["jobs-"+jobs[i].Name].GetRect()
		x2, _, _, _ := boxes["jobs-"+jobs[k].Name].GetRect()
		stageWidth := x2 - x1 - w
		switch {
		case stageWidth <= 3:
			padding = 1
		case stageWidth <= 6:
			padding = 2
		case stageWidth > 6:
			padding = 3
		}
	}
	for i, k := 0, 1; k < len(jobs); i, k = i+1, k+1 {
		v1 := boxes["jobs-"+jobs[i].Name]
		v2 := boxes["jobs-"+jobs[k].Name]
		link(screen, v1.Box, v2.Box, padding,
			jobs[i].Stage == jobs[0].Stage,           // is first stage?
			jobs[i].Stage == jobs[len(jobs)-1].Stage) // is last stage?
	}
	return nil
}

func link(
	screen tcell.Screen,
	v1 *tview.Box,
	v2 *tview.Box,
	padding int,
	firstStage, lastStage bool,
) {
	x1, y1, w, h := v1.GetRect()
	x2, y2, _, _ := v2.GetRect()

	dx, dy := x2-x1, y2-y1

	p := padding

	// drawing stages
	if dx != 0 {
		hline(screen, x1+w, y2+h/2, dx-w)
		if dy != 0 {
			// dy != 0 means the last stage had multiple jobs
			screen.SetContent(x1+w+p-1, y2+h/2, '╦', nil, tcell.StyleDefault)
		}
		return
	}

	// Drawing a job in the same stage
	// left of view
	if !firstStage {
		s, _, _ := screen.Get(x2-p, y1+h/2)
		if r, _ := utf8.DecodeRuneInString(s); r == '╚' {
			screen.SetContent(x2-p, y1+h/2, '╠', nil, tcell.StyleDefault)
		} else {
			screen.SetContent(x2-p, y1+h/2, '╦', nil, tcell.StyleDefault)
		}

		for i := 1; i < p; i++ {
			screen.SetContent(x2-i, y2+h/2, '═', nil, tcell.StyleDefault)
		}
		screen.SetContent(x2-p, y2+h/2, '╚', nil, tcell.StyleDefault)

		vline(screen, x2-p, y1+h-1, dy-1)
	}
	// right of view
	if !lastStage {
		s, _, _ := screen.Get(x2+w+p-1, y1+h/2)
		if r, _ := utf8.DecodeRuneInString(s); r == '┛' {
			screen.SetContent(x2+w+p-1, y1+h/2, '╣', nil, tcell.StyleDefault)
		}
		for i := range p - 1 {
			screen.SetContent(x2+w+i, y2+h/2, '═', nil, tcell.StyleDefault)
		}
		screen.SetContent(x2+w+p-1, y2+h/2, '╝', nil, tcell.StyleDefault)

		vline(screen, x2+w+p-1, y1+h-1, dy-1)
	}
}

func hline(screen tcell.Screen, x, y, l int) {
	for i := range l {
		screen.SetContent(x+i, y, '═', nil, tcell.StyleDefault)
	}
}

func vline(screen tcell.Screen, x, y, l int) {
	for i := range l {
		screen.SetContent(x, y+i, '║', nil, tcell.StyleDefault)
	}
}

// latestJobs returns a list of unique jobs favoring the last stage+name
// version of a job in the provided list
func latestJobs(jobs []*ViewJob) []*ViewJob {
	var (
		lastJob = make(map[string]*ViewJob, len(jobs))
		dupIdx  = -1
	)
	for i, j := range jobs {
		_, ok := lastJob[j.Stage+j.Name]
		if dupIdx == -1 && ok {
			dupIdx = i
		}
		// always want the latest job
		lastJob[j.Stage+j.Name] = j
	}
	if dupIdx == -1 {
		dupIdx = len(jobs)
	}
	// first duplicate marks where retries begin
	outJobs := make([]*ViewJob, dupIdx)
	for i := range outJobs {
		j := jobs[i]
		outJobs[i] = lastJob[j.Stage+j.Name]
	}

	return outJobs
}
