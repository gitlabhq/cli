//go:build !integration

package cmdutils_test

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
)

func TestPipelineInputsFromFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		want    gitlab.PipelineInputsOption
		wantErr bool
	}{
		{
			name: "flag unset",
			args: []string{},
			want: nil,
		},
		{
			name: "shorthand",
			args: []string{"-i", "key:val"},
			want: gitlab.PipelineInputsOption{
				"key": gitlab.NewPipelineInputValue("val"),
			},
		},
		{
			name: "typed inputs",
			args: []string{
				"--input", "environment:string(production)",
				"--input", "replicas:int(3)",
				"--input", "dry_run:bool(false)",
				"--input", "regions:array(us-east,eu-west)",
				"--input", "err_rate:float(0.01)",
			},
			want: gitlab.PipelineInputsOption{
				"environment": gitlab.NewPipelineInputValue("production"),
				"replicas":    gitlab.NewPipelineInputValue(3),
				"dry_run":     gitlab.NewPipelineInputValue(false),
				"regions":     gitlab.NewPipelineInputValue([]string{"us-east", "eu-west"}),
				"err_rate":    gitlab.NewPipelineInputValue(0.01),
			},
		},
		{
			name: "empty array",
			args: []string{"-i", "key:array()"},
			want: gitlab.PipelineInputsOption{
				"key": gitlab.NewPipelineInputValue([]string{}),
			},
		},
		{
			name: "array with empty string",
			args: []string{"-i", "key:array(,)"},
			want: gitlab.PipelineInputsOption{
				"key": gitlab.NewPipelineInputValue([]string{""}),
			},
		},
		{
			name: "array with trailing comma",
			args: []string{"-i", "key:array(foo,)"},
			want: gitlab.PipelineInputsOption{
				"key": gitlab.NewPipelineInputValue([]string{"foo"}),
			},
		},
		{
			name: "array with empty string at the end",
			args: []string{"-i", "key:array(foo,,)"},
			want: gitlab.PipelineInputsOption{
				"key": gitlab.NewPipelineInputValue([]string{"foo", ""}),
			},
		},
		{
			name:    "empty key",
			args:    []string{"-i", ":invalid"},
			wantErr: true,
		},
		{
			name:    "missing colon",
			args:    []string{"-i", "invalid"},
			wantErr: true,
		},
		{
			name:    "invalid integer",
			args:    []string{"-i", "key:int(invalid)"},
			wantErr: true,
		},
		{
			name:    "invalid float",
			args:    []string{"-i", "key:float(invalid)"},
			wantErr: true,
		},
		{
			name:    "invalid bool",
			args:    []string{"-i", "key:bool(invalid)"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{}
			cmdutils.AddPipelineInputsFlag(cmd)
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}

			got, gotErr := cmdutils.PipelineInputsFromFlags(cmd)
			if gotErr != nil {
				if !tc.wantErr {
					t.Errorf("PipelineInputsFromFlags() failed: %v", gotErr)
				}
				return
			}

			if tc.wantErr {
				t.Fatal("PipelineInputsFromFlags() succeeded unexpectedly")
			}

			if !reflect.DeepEqual(tc.want, got) {
				t.Errorf("PipelineInputsFromFlags() mismatch:\ngot:\n%+v\n\nwant:\n%+v\n", got, tc.want)
			}
		})
	}
}
