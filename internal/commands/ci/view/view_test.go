//go:build !integration

package view

import (
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func assertScreen(t *testing.T, screen tcell.Screen, expected []string) {
	t.Helper()

	sx, sy := screen.Size()
	assert.Equal(t, len(expected), sy)
	assert.Equal(t, len([]rune(expected[0])), sx)
	actual := make([]string, sy)
	for y, str := range expected {
		runes := make([]rune, len(str))
		row := []rune(str)
		for x, expectedRune := range row {
			s, _, _ := screen.Get(x, y)
			runes[x], _ = utf8.DecodeRuneInString(s)
			_ = expectedRune
			// assert.Equal(t, expectedRune, r, "%s != %s at (%d,%d)",
			//	strconv.QuoteRune(expectedRune), strconv.QuoteRune(r), x, y)
		}

		actual[y] = strings.TrimRight(string(runes), string('\x00'))
		assert.Equal(t, str, actual[y])
	}
	t.Logf("Expected w: %d l: %d", len([]rune(expected[0])), len(expected))
	for _, str := range expected {
		t.Log(str)
	}
	t.Logf("Actual w: %d l: %d", sx, sy)
	for _, str := range actual {
		t.Log(str)
	}
}

func Test_line(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		lineF    func(screen tcell.Screen, x, y, l int)
		x, y, l  int
		expected []string
	}{
		{
			"hline",
			hline,
			2, 2, 5,
			[]string{
				"          ",
				"          ",
				"  ═════   ",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
			},
		},
		{
			"hline overflow",
			hline,
			2, 2, 10,
			[]string{
				"          ",
				"          ",
				"  ════════",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
			},
		},
		{
			"vline",
			vline,
			2, 2, 5,
			[]string{
				"          ",
				"          ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"          ",
				"          ",
				"          ",
			},
		},
		{
			"vline overflow",
			vline,
			2, 2, 10,
			[]string{
				"          ",
				"          ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
				"  ║       ",
			},
		},
	}

	for _, test := range tests {
		screen := tcell.NewSimulationScreen("UTF-8")
		err := screen.Init()
		if err != nil {
			t.Fatal(err)
		}
		// Set screen to matrix size
		screen.SetSize(len(test.expected), len(test.expected[0]))

		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			test.lineF(screen, test.x, test.y, test.l)
			screen.Show()
			assertScreen(t, screen, test.expected)
		})
	}
}

func testbox(x, y, w, h int) *tview.TextView { //nolint:unparam
	b := tview.NewTextView()
	b.
		SetBackgroundColor(tcell.ColorDefault).
		SetBorder(true).
		SetRect(x, y, w, h)
	return b
}

func Test_Link(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc        string
		b1, b2      *tview.Box
		first, last bool
		expected    []string
	}{
		{
			"first stage",
			testbox(2, 1, 3, 3).Box, testbox(2, 5, 3, 3).Box,
			true, false,
			[]string{
				"          ",
				"  ┌─┐     ",
				"  │ │     ",
				"  └─┘ ║   ",
				"      ║   ",
				"  ┌─┐ ║   ",
				"  │ │═╝   ",
				"  └─┘     ",
				"          ",
				"          ",
			},
		},
		{
			"last stage",
			testbox(5, 1, 3, 3).Box, testbox(5, 5, 3, 3).Box,
			false, true,
			[]string{
				"          ",
				"     ┌─┐  ",
				"   ╦ │ │  ",
				"   ║ └─┘  ",
				"   ║      ",
				"   ║ ┌─┐  ",
				"   ╚═│ │  ",
				"     └─┘  ",
				"          ",
				"          ",
			},
		},
		{
			"cross stage",
			testbox(1, 1, 3, 3).Box, testbox(7, 1, 3, 3).Box,
			false, false,
			[]string{
				"          ",
				" ┌─┐   ┌─┐",
				" │ │═══│ │",
				" └─┘   └─┘",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
				"          ",
			},
		},
	}

	for _, test := range tests {
		screen := tcell.NewSimulationScreen("UTF-8")
		err := screen.Init()
		if err != nil {
			t.Fatal(err)
		}
		// Set screen to matrix size
		screen.SetSize(len(test.expected), len(test.expected[0]))

		test.b1.Draw(screen)
		test.b2.Draw(screen)

		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			link(screen, test.b1, test.b2, 2, test.first, test.last)
			screen.Show()
			assertScreen(t, screen, test.expected)
		})
	}
}

func Test_LinkJobs(t *testing.T) {
	expected := []string{
		"                 ",
		" ┌─┐   ┌─┐   ┌─┐ ",
		" │ │╦═╦│ │╦═╦│ │ ",
		" └─┘║ ║└─┘║ ║└─┘ ",
		"    ║ ║   ║ ║    ",
		" ┌─┐║ ║┌─┐║ ║┌─┐ ",
		" │ │╝ ╠│ │╝ ╚│ │ ",
		" └─┘║ ║└─┘║  └─┘ ",
		"    ║ ║   ║      ",
		" ┌─┐║ ║┌─┐║      ",
		" │ │╝ ╚│ │╝      ",
		" └─┘║  └─┘       ",
		"    ║            ",
		" ┌─┐║            ",
		" │ │╝            ",
		" └─┘             ",
		"                 ",
	}
	jobs := []*ViewJob{
		{
			Name:  "stage1-job1",
			Stage: "stage1",
			Kind:  Job,
		},
		{
			Name:  "stage1-job2",
			Stage: "stage1",
			Kind:  Job,
		},
		{
			Name:  "stage1-job3",
			Stage: "stage1",
			Kind:  Job,
		},
		{
			Name:  "stage1-job4",
			Stage: "stage1",
			Kind:  Job,
		},
		{
			Name:  "stage2-job1",
			Stage: "stage2",
			Kind:  Bridge,
		},
		{
			Name:  "stage2-job2",
			Stage: "stage2",
			Kind:  Job,
		},
		{
			Name:  "stage2-job3",
			Stage: "stage2",
			Kind:  Job,
		},
		{
			Name:  "stage3-job1",
			Stage: "stage3",
			Kind:  Job,
		},
		{
			Name:  "stage3-job2",
			Stage: "stage3",
			Kind:  Job,
		},
	}
	boxes := map[string]*tview.TextView{
		"jobs-stage1-job1": testbox(1, 1, 3, 3),
		"jobs-stage1-job2": testbox(1, 5, 3, 3),
		"jobs-stage1-job3": testbox(1, 9, 3, 3),
		"jobs-stage1-job4": testbox(1, 13, 3, 3),

		"jobs-stage2-job1": testbox(7, 1, 3, 3),
		"jobs-stage2-job2": testbox(7, 5, 3, 3),
		"jobs-stage2-job3": testbox(7, 9, 3, 3),

		"jobs-stage3-job1": testbox(13, 1, 3, 3),
		"jobs-stage3-job2": testbox(13, 5, 3, 3),
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	err := screen.Init()
	if err != nil {
		t.Fatal(err)
	}
	// Set screen to matrix size
	screen.SetSize(len(expected), len(expected[0]))

	for _, b := range boxes {
		b.Draw(screen)
	}

	err = linkJobs(screen, jobs, boxes)
	if err != nil {
		t.Fatal(err)
	}

	screen.Show()
	assertScreen(t, screen, expected)
}

func Test_LinkJobsNegative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc  string
		jobs  []*ViewJob
		boxes map[string]*tview.TextView
	}{
		{
			"determinePadding -- first job missing",
			[]*ViewJob{
				{
					Name:  "stage1-job1",
					Stage: "stage1",
					Kind:  Job,
				},
			},
			map[string]*tview.TextView{
				"jobs-stage2-job1": testbox(1, 5, 3, 3),
				"jobs-stage2-job2": testbox(1, 9, 3, 3),
			},
		},
		{
			"determinePadding -- second job missing",
			[]*ViewJob{
				{
					Name:  "stage1-job1",
					Stage: "stage1",
					Kind:  Job,
				},
				{
					Name:  "stage2-job1",
					Stage: "stage2",
					Kind:  Job,
				},
				{
					Name:  "stage2-job2",
					Stage: "stage2",
					Kind:  Job,
				},
			},
			map[string]*tview.TextView{
				"jobs-stage1-job1": testbox(1, 1, 3, 3),
				"jobs-stage2-job2": testbox(1, 9, 3, 3),
			},
		},
		{
			"Link -- third job missing",
			[]*ViewJob{
				{
					Name:  "stage1-job1",
					Stage: "stage1",
					Kind:  Job,
				},
				{
					Name:  "stage2-job1",
					Stage: "stage2",
					Kind:  Job,
				},
				{
					Name:  "stage2-job2",
					Stage: "stage2",
					Kind:  Job,
				},
			},
			map[string]*tview.TextView{
				"jobs-stage1-job1": testbox(1, 1, 3, 3),
				"jobs-stage2-job1": testbox(1, 5, 3, 3),
			},
		},
	}
	for _, test := range tests {
		screen := tcell.NewSimulationScreen("UTF-8")
		err := screen.Init()
		if err != nil {
			t.Fatal(err)
		}
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			assert.Error(t, linkJobs(screen, test.jobs, test.boxes))
		})
	}
}

func Test_jobsView(t *testing.T) {
	expected := []string{
		"  ┌────────────────────┐      ┌────────────────────┐      ┌────────────────────┐        ",
		"  │       Stage1       │      │       Stage2       │      │       Stage3       │        ",
		"  └────────────────────┘      └────────────────────┘      └────────────────────┘        ",
		"                                                                                        ",
		"  ╔✔ stage1-job1-reall…╗      ┌● stage2-job1[step]─┐      ┌───■ stage3-job1────┐        ",
		"  ║                    ║      │                    │      │                    │        ",
		"  ║             01m 01s║═╦══╦═│                    │═╦══╦═│                    │        ",
		"  ╚════════════════════╝ ║  ║ └────────────────────┘ ║  ║ └────────────────────┘        ",
		"                         ║  ║                        ║  ║                               ",
		"  ┌───✔ stage1-job2────┐ ║  ║ ┌───● stage2-job2────┐ ║  ║ ┌───■ stage3-job2────┐        ",
		"  │                    │ ║  ║ │                    │ ║  ║ │                   »│        ",
		"  │                    │═╝  ╠═│                    │═╝  ╚═│                    │        ",
		"  └────────────────────┘ ║  ║ └────────────────────┘ ║    └────────────────────┘        ",
		"                         ║  ║                        ║                                  ",
		"  ┌───✔ stage1-job3────┐ ║  ║ ┌───● stage2-job3────┐ ║                                  ",
		"  │                    │ ║  ║ │                    │ ║                                  ",
		"  │                    │═╝  ╚═│                    │═╝                                  ",
		"  └────────────────────┘ ║    └────────────────────┘                                    ",
		"                         ║                                                              ",
		"  ┌───✘ stage1-job4────┐ ║                                                              ",
		"  │                    │ ║                                                              ",
		"  │                    │═╝                                                              ",
		"  └────────────────────┘                                                                ",
		"                                                                                        ",
		"                                                                                        ",
		"                                                                                        ",
	}
	now := time.Now()
	past := now.Add(time.Second * -61)
	jobs := []*ViewJob{
		{
			Name:       "stage1-job1-really-long",
			Stage:      "stage1",
			Status:     "success",
			StartedAt:  &past, // relies on test running in <1s we'll see how it goes
			FinishedAt: &now,
		},
		{
			Name:   "stage1-job2",
			Stage:  "stage1",
			Status: "success",
		},
		{
			Name:   "stage1-job3",
			Stage:  "stage1",
			Status: "success",
		},
		{
			Name:   "stage1-job4",
			Stage:  "stage1",
			Status: "failed",
		},
		{
			Name:   "stage2-job1[step]",
			Stage:  "stage2",
			Status: "running",
		},
		{
			Name:   "stage2-job2",
			Stage:  "stage2",
			Status: "running",
		},
		{
			Name:   "stage2-job3",
			Stage:  "stage2",
			Status: "pending",
		},
		{
			Name:   "stage3-job1",
			Stage:  "stage3",
			Status: "manual",
		},
		{
			Name:   "stage3-job2",
			Stage:  "stage3",
			Status: "manual",
			Kind:   Bridge,
		},
	}

	boxes = make(map[string]*tview.TextView)
	jobsCh := make(chan []*ViewJob)
	inputCh := make(chan struct{})
	root := tview.NewPages()
	root.
		SetBackgroundColor(tcell.ColorDefault).
		SetBorderPadding(1, 1, 2, 2)

	screen := tcell.NewSimulationScreen("UTF-8")
	err := screen.Init()
	if err != nil {
		t.Fatal(err)
	}
	// Set screen to matrix size
	screen.SetSize(len([]rune(expected[0])), len(expected))
	w, h := screen.Size()
	root.SetRect(0, 0, w, h)

	go func() {
		jobsCh <- jobs
	}()
	root.Box.Focus(nil)
	jobsView(t.Context(), nil, jobsCh, inputCh, root, nil, "", "")
	root.Focus(func(p tview.Primitive) { p.Focus(nil) })
	root.Draw(screen)
	linkJobsView(nil)(screen)
	screen.Sync()
	assertScreen(t, screen, expected)
}

func Test_latestJobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		jobs     []*ViewJob
		expected []*ViewJob
	}{
		{
			desc: "no newer jobs",
			jobs: []*ViewJob{
				{
					ID:    1,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
				{
					ID:    2,
					Name:  "stage1-job2",
					Stage: "stage1",
				},
				{
					ID:    3,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
			},
			expected: []*ViewJob{
				{
					ID:    1,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
				{
					ID:    2,
					Name:  "stage1-job2",
					Stage: "stage1",
				},
				{
					ID:    3,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
			},
		},
		{
			desc: "1 newer",
			jobs: []*ViewJob{
				{
					ID:    1,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
				{
					ID:    2,
					Name:  "stage1-job2",
					Stage: "stage1",
				},
				{
					ID:    3,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
				{
					ID:    4,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
			},
			expected: []*ViewJob{
				{
					ID:    4,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
				{
					ID:    2,
					Name:  "stage1-job2",
					Stage: "stage1",
				},
				{
					ID:    3,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
			},
		},
		{
			desc: "2 newer",
			jobs: []*ViewJob{
				{
					ID:    1,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
				{
					ID:    2,
					Name:  "stage1-job2",
					Stage: "stage1",
				},
				{
					ID:    3,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
				{
					ID:    4,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
				{
					ID:    5,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
			},
			expected: []*ViewJob{
				{
					ID:    5,
					Name:  "stage1-job1",
					Stage: "stage1",
				},
				{
					ID:    2,
					Name:  "stage1-job2",
					Stage: "stage1",
				},
				{
					ID:    4,
					Name:  "stage1-job3",
					Stage: "stage1",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			jobs := latestJobs(test.jobs)
			assert.Equal(t, test.expected, jobs)
		})
	}
}

func Test_adjacentStages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc         string
		stage        string
		jobs         []*ViewJob
		expectedPrev string
		expectedNext string
	}{
		{
			desc:         "no jobs",
			stage:        "1",
			jobs:         []*ViewJob{},
			expectedPrev: "",
			expectedNext: "",
		},
		{
			desc:  "first stage",
			stage: "1",
			jobs: []*ViewJob{
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "2",
				},
			},
			expectedPrev: "1",
			expectedNext: "2",
		},
		{
			desc:  "mid stage",
			stage: "2",
			jobs: []*ViewJob{
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "2",
				},
				{
					Stage: "2",
				},
				{
					Stage: "3",
				},
			},
			expectedPrev: "1",
			expectedNext: "3",
		},
		{
			desc:  "last stage",
			stage: "3",
			jobs: []*ViewJob{
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "2",
				},
				{
					Stage: "2",
				},
				{
					Stage: "3",
				},
			},
			expectedPrev: "2",
			expectedNext: "3",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			prev, next := adjacentStages(test.jobs, test.stage)
			assert.Equal(t, test.expectedPrev, prev)
			assert.Equal(t, test.expectedNext, next)
		})
	}
}

func Test_stageBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc                         string
		stage                        string
		jobs                         []*ViewJob
		expectedLower, expectedUpper int
	}{
		{
			"no jobs",
			"1",
			[]*ViewJob{},
			0, 0,
		},
		{
			"first stage",
			"1",
			[]*ViewJob{
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "2",
				},
			},
			0, 2,
		},
		{
			"mid stage",
			"2",
			[]*ViewJob{
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "2",
				},
				{
					Stage: "2",
				},
				{
					Stage: "3",
				},
			},
			3, 4,
		},
		{
			"last stage",
			"3",
			[]*ViewJob{
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "1",
				},
				{
					Stage: "2",
				},
				{
					Stage: "2",
				},
				{
					Stage: "3",
				},
			},
			5, 5,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			lower, upper := stageBounds(test.jobs, test.stage)
			assert.Equal(t, test.expectedLower, lower)
			assert.Equal(t, test.expectedUpper, upper)
		})
	}
}

func Test_handleNavigation(t *testing.T) {
	t.Parallel()

	jobs := []*ViewJob{
		{
			Name:   "stage1-job1-really-long",
			Stage:  "stage1",
			Status: "success",
		},
		{
			Name:   "stage1-job2",
			Stage:  "stage1",
			Status: "success",
		},
		{
			Name:   "stage1-job3",
			Stage:  "stage1",
			Status: "success",
		},
		{
			Name:   "stage1-job4",
			Stage:  "stage1",
			Status: "failed",
		},
		{
			Name:   "stage2-job1",
			Stage:  "stage2",
			Status: "running",
		},
		{
			Name:   "stage2-job2",
			Stage:  "stage2",
			Status: "running",
		},
		{
			Name:   "stage2-job3",
			Stage:  "stage2",
			Status: "pending",
		},
		{
			Name:   "stage3-job1",
			Stage:  "stage3",
			Status: "manual",
		},
		{
			Name:   "stage3-job2",
			Stage:  "stage3",
			Status: "manual",
		},
	}
	tests := []struct {
		desc     string
		input    []*tcell.EventKey
		expected int
	}{
		{
			"do nothing",
			[]*tcell.EventKey{},
			0,
		},
		{
			"arrows down",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
			},
			3,
		},
		{
			"arrows down bottom boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
			},
			3,
		},
		{
			"arrows down bottom middle boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
			},
			6,
		},
		{
			"arrows down last (3rd) column bottom boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
			},
			8,
		},
		{
			"arrows down persistent depth bottom boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone),
			},
			3,
		},
		{
			"arrows left boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone),
			},
			0,
		},
		{
			"arrows up boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone),
			},
			0,
		},
		{
			"arrows right boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone),
			},
			7,
		},
		{
			"hjkl down",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
			},
			3,
		},
		{
			"hjkl down bottom boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
			},
			3,
		},
		{
			"hjkl down bottom middle boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
			},
			6,
		},
		{
			"hjkl down last (3rd) column bottom boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
			},
			8,
		},
		{
			"hjkl down persistent depth bottom boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone),
			},
			3,
		},
		{
			"hjkl left boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone),
			},
			0,
		},
		{
			"hjkl up boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone),
			},
			0,
		},
		{
			"hjkl right boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
			},
			7,
		},
		{
			"G boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'G', tcell.ModNone),
			},
			3,
		},
		{
			"Gg boundary",
			[]*tcell.EventKey{
				tcell.NewEventKey(tcell.KeyRune, 'G', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone),
			},
			0,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			var navi navigator
			for _, e := range test.input {
				navi.Navigate(jobs, e)
			}
			assert.Equal(t, test.expected, navi.idx)
		})
	}
}

// Test_navigatorSurvivesPipelineSwitch verifies that the navigator does not
// panic with "index out of range" when applied to a smaller jobs slice than
// the one its idx/depth were last sized against — the crash reported in #8313
// when entering a child pipeline from a bridge job.
func Test_navigatorSurvivesPipelineSwitch(t *testing.T) {
	t.Parallel()

	parentJobs := []*ViewJob{
		{Name: "a1", Stage: "build", Status: "success"},
		{Name: "a2", Stage: "build", Status: "success"},
		{Name: "a3", Stage: "build", Status: "success"},
		{Name: "b1", Stage: "test", Status: "success"},
		{Name: "b2", Stage: "test", Status: "success"},
		{Name: "c1", Stage: "deploy", Status: "success"},
		{Name: "c2", Stage: "deploy", Status: "success"},
	}
	childJobs := []*ViewJob{
		{Name: "x1", Stage: "build", Status: "success"},
		{Name: "x2", Stage: "build", Status: "success"},
	}

	var navi navigator
	// Park the cursor at the last job of the parent (idx 6).
	navi.Navigate(parentJobs, tcell.NewEventKey(tcell.KeyRune, 'G', tcell.ModNone))
	navi.Navigate(parentJobs, tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))
	navi.Navigate(parentJobs, tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))
	require.GreaterOrEqual(t, navi.idx, len(childJobs), "test setup: idx should exceed child size")

	// Without a reset, the next Navigate against childJobs would panic at
	// `jobs[n.idx].Stage`. The defensive clamp must keep us inside bounds.
	require.NotPanics(t, func() {
		got := navi.Navigate(childJobs, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
		assert.NotNil(t, got)
	})
	assert.Less(t, navi.idx, len(childJobs))
}

// Test_curPipeline_nilLastPipeline verifies that curPipeline returns an error
// instead of panicking when the stack is empty and the commit has no
// LastPipeline. Before the fix, this dereferenced a nil pointer.
func Test_curPipeline_nilLastPipeline(t *testing.T) {
	// Cannot run in parallel: mutates the package-level `pipelines` global.
	pipelines = nil

	t.Run("stack empty and LastPipeline nil returns error", func(t *testing.T) {
		commit := &gitlab.Commit{ID: "deadbeef", LastPipeline: nil}
		got, err := curPipeline(commit)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deadbeef")
		assert.Equal(t, gitlab.PipelineInfo{}, got)
	})

	t.Run("stack empty falls back to LastPipeline", func(t *testing.T) {
		pipelines = nil
		commit := &gitlab.Commit{
			ID:           "deadbeef",
			LastPipeline: &gitlab.PipelineInfo{ID: 42, ProjectID: 7},
		}
		got, err := curPipeline(commit)
		require.NoError(t, err)
		assert.Equal(t, int64(42), got.ID)
	})

	t.Run("stack non-empty ignores LastPipeline", func(t *testing.T) {
		pipelines = []gitlab.PipelineInfo{{ID: 99, ProjectID: 7}}
		t.Cleanup(func() { pipelines = nil })
		commit := &gitlab.Commit{ID: "deadbeef", LastPipeline: nil}
		got, err := curPipeline(commit)
		require.NoError(t, err)
		assert.Equal(t, int64(99), got.ID)
	})
}

func Test_bracketEscaper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		input    string
		expected string
	}{
		{
			desc:     "no brackets",
			input:    "simple text",
			expected: "simple text",
		},
		{
			desc:     "literal brackets [MASKED]",
			input:    "value is [MASKED]",
			expected: "value is [MASKED[]",
		},
		{
			desc:     "ANSI escape sequence preserved",
			input:    "\x1b[32;1mgreen text\x1b[0m",
			expected: "\x1b[32;1mgreen text\x1b[0m",
		},
		{
			desc:     "ANSI with literal brackets",
			input:    "\x1b[32;1m$ echo \"test\"\x1b[0m\nvalue is [MASKED]\n",
			expected: "\x1b[32;1m$ echo \"test\"\x1b[0m\nvalue is [MASKED[]\n",
		},
		{
			desc:     "multiple literal brackets",
			input:    "[MASKED] and [HIDDEN]",
			expected: "[MASKED[] and [HIDDEN[]",
		},
		{
			desc:     "complex trace with section markers",
			input:    "Compile complete.\n\x1b[32;1m$ echo \"MASKED variables's value is ${TEST_MASKED}\"\x1b[0m\nMASKED variables's value is [MASKED]\n",
			expected: "Compile complete.\n\x1b[32;1m$ echo \"MASKED variables's value is ${TEST_MASKED}\"\x1b[0m\nMASKED variables's value is [MASKED[]\n",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			t.Parallel()

			var output strings.Builder
			escaper := &bracketEscaper{Writer: &output}

			n, err := escaper.Write([]byte(test.input))

			require.NoError(t, err)
			assert.Equal(t, len(test.input), n, "should return number of input bytes consumed")
			assert.Equal(t, test.expected, output.String())
		})
	}
}

func TestNormalizeCtrlM(t *testing.T) {
	t.Parallel()

	event := normalizeCtrlM(tcell.NewEventKey(tcell.KeyCtrlM, 0, tcell.ModNone))

	assert.Equal(t, tcell.KeyEnter, event.Key())
	assert.Equal(t, tcell.ModNone, event.Modifiers())
}

// Test_bridgeWithoutDownstreamPipeline tests that we correctly handle bridge jobs
// without downstream pipelines (fixes crash in work item #7372)
func Test_bridgeWithoutDownstreamPipeline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bridge *gitlab.Bridge
	}{
		{
			name: "manual bridge without downstream pipeline",
			bridge: &gitlab.Bridge{
				ID:                 1,
				Name:               "trigger-job",
				Status:             "manual",
				Stage:              "deploy",
				DownstreamPipeline: nil, // This nil caused crash in #7372
			},
		},
		{
			name: "created bridge without downstream pipeline",
			bridge: &gitlab.Bridge{
				ID:                 2,
				Name:               "trigger-pipeline",
				Status:             "created",
				Stage:              "deploy",
				DownstreamPipeline: nil,
			},
		},
		{
			name: "skipped bridge without downstream pipeline",
			bridge: &gitlab.Bridge{
				ID:                 3,
				Name:               "conditional-trigger",
				Status:             "skipped",
				Stage:              "deploy",
				DownstreamPipeline: nil,
			},
		},
		{
			name: "bridge with downstream pipeline",
			bridge: &gitlab.Bridge{
				ID:     4,
				Name:   "active-trigger",
				Status: "success",
				Stage:  "deploy",
				DownstreamPipeline: &gitlab.PipelineInfo{
					ID:     123,
					Status: "running",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			viewJob := ViewJobFromBridge(tt.bridge)

			// Verify the ViewJob was created correctly
			assert.NotNil(t, viewJob)
			assert.Equal(t, tt.bridge.Name, viewJob.Name)
			assert.Equal(t, tt.bridge.Status, viewJob.Status)
			assert.Equal(t, Bridge, viewJob.Kind)
			assert.NotNil(t, viewJob.OriginalBridge)

			// Verify DownstreamPipeline state matches expectation
			if tt.bridge.DownstreamPipeline == nil {
				assert.Nil(t, viewJob.OriginalBridge.DownstreamPipeline,
					"Bridge job without downstream pipeline should have nil DownstreamPipeline")
			} else {
				assert.NotNil(t, viewJob.OriginalBridge.DownstreamPipeline,
					"Bridge job with downstream pipeline should have non-nil DownstreamPipeline")
			}
		})
	}
}

func TestCIView(t *testing.T) {
	createdAt, _ := time.Parse(time.RFC3339, "2025-10-28T16:52:39.000+01:00")

	type testCase struct {
		name           string
		cli            string
		setupMock      func(tc *gitlabtesting.TestClient)
		expectedOutput string
	}

	tests := []testCase{
		{
			name: "view ci pipeline on web for a given branch",
			cli:  "--web --branch foo",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.Pipeline{
						ID:        8,
						Ref:       "foo",
						SHA:       "2dc6aa325a317eda67812f05600bdf0fcdc70ab0",
						Status:    "created",
						WebURL:    "https://gitlab.com/OWNER/REPO/-/pipelines/225",
						CreatedAt: &createdAt,
					}, nil, nil)

				// GetPipelineWithFallback checks if pipeline has jobs
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(8), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{{ID: 1}}, nil, nil)

				tc.MockCommits.EXPECT().
					GetCommit("OWNER/REPO", "2dc6aa325a317eda67812f05600bdf0fcdc70ab0", gomock.Any(), gomock.Any()).
					Return(&gitlab.Commit{
						ID: "2dc6aa325a317eda67812f05600bdf0fcdc70ab0",
						LastPipeline: &gitlab.PipelineInfo{
							ID:        8,
							Ref:       "foo",
							SHA:       "2dc6aa325a317eda67812f05600bdf0fcdc70ab0",
							Status:    "created",
							WebURL:    "https://gitlab.com/OWNER/REPO/-/pipelines/225",
							CreatedAt: &createdAt,
						},
						Status: new(gitlab.Running),
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.com/OWNER/REPO/-/pipelines/225 in your browser.\n",
		},
		{
			name: "view ci pipeline on web for a given pipeline id",
			cli:  "--web --pipelineid 5",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(5), gomock.Any()).
					Return(&gitlab.Pipeline{
						ID:        5,
						WebURL:    "https://gitlab.com/OWNER/REPO/-/pipelines/5",
						CreatedAt: &createdAt,
						SHA:       "2dc6aa325a317eda67812f05600bdf0fcdc70ab0",
					}, nil, nil)

				tc.MockCommits.EXPECT().
					GetCommit("OWNER/REPO", "2dc6aa325a317eda67812f05600bdf0fcdc70ab0", gomock.Any(), gomock.Any()).
					Return(&gitlab.Commit{
						ID: "6104942438c14ec7bd21c6cd5bd995272b3faff6",
						LastPipeline: &gitlab.PipelineInfo{
							ID:        5,
							Ref:       "main",
							SHA:       "2dc6aa325a317eda67812f05600bdf0fcdc70ab0",
							Status:    "created",
							WebURL:    "https://gitlab.com/OWNER/REPO/-/pipelines/225",
							CreatedAt: &createdAt,
						},
						Status: new(gitlab.Running),
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.com/OWNER/REPO/-/pipelines/5 in your browser.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
				return &test.OutputStub{}
			})
			defer restoreCmd()

			exec := cmdtest.SetupCmdForTest(t, NewCmdView, true,
				cmdtest.WithGitLabClient(testClient.Client),
			)
			output, err := exec(tc.cli)

			if assert.NoErrorf(t, err, "error running command `ci view %s`: %v", tc.cli, err) {
				assert.Empty(t, output.String())
				assert.Equal(t, tc.expectedOutput, output.Stderr())
			}
		})
	}
}
