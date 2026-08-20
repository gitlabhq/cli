package cmdutils

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

// PipelineInputsDescription documents the --input flag; commands append it to
// their Long help text.
var PipelineInputsDescription = heredoc.Docf(`
	Specify one or more pipeline inputs using the %[1]s-i%[1]s or %[1]s--input%[1]s flag for each
	input. Each input flag uses the format %[1]skey:value%[1]s.

	The values are typed and will default to %[1]sstring%[1]s unless a type is explicitly
	specified. To specify a type, use the %[1]stype(value)%[1]s syntax. For example,
	%[1]skey:string(value)%[1]s will pass the string %[1]svalue%[1]s as the input.

	Valid types are:

	- %[1]sstring%[1]s: A string value. This is the default type. For example, %[1]skey:string(value)%[1]s.
	- %[1]sint%[1]s: An integer value. For example, %[1]skey:int(42)%[1]s.
	- %[1]sfloat%[1]s: A floating-point value. For example, %[1]skey:float(3.14)%[1]s.
	- %[1]sbool%[1]s: A boolean value. For example, %[1]skey:bool(true)%[1]s.
	- %[1]sarray%[1]s: An array of strings. For example, %[1]skey:array(foo,bar)%[1]s.

	An array of strings can be specified with a trailing comma. For example,
	%[1]skey:array(foo,bar,)%[1]s will pass the array %[1]s[foo, bar]%[1]s. %[1]sarray()%[1]s specifies an
	empty array. To pass an array with the empty string, use %[1]sarray(,)%[1]s.

	Value arguments containing parentheses should be escaped from the shell with
	quotes. For example, %[1]s--input key:array(foo,bar)%[1]s should be written as
	%[1]s--input 'key:array(foo,bar)'%[1]s.
`, "`")

// AddPipelineInputsFlag adds a flag to a cobra command for pipeline inputs.
func AddPipelineInputsFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayP("input", "i", nil, "Pass inputs to pipeline in format '<key>:<value>'. Cannot be used for merge request pipelines. See documentation for examples.")
}

// PipelineInputsFromFlags creates a gitlab.PipelineInputsOption from the "input" command line flag.
//
// Returns a nil map if the flag is not set.
func PipelineInputsFromFlags(cmd *cobra.Command) (gitlab.PipelineInputsOption, error) {
	flagVal, err := cmd.Flags().GetStringArray("input")
	if err != nil {
		return nil, err
	}

	if len(flagVal) == 0 {
		return nil, nil
	}

	inputs := make(map[string]gitlab.PipelineInputValueInterface, len(flagVal))

	for _, raw := range flagVal {
		key, val, err := parsePipelineInput(raw)
		if err != nil {
			return nil, err
		}

		inputs[key] = val
	}

	return inputs, nil
}

var typedInputValueRE = regexp.MustCompile(`^(int|float|string|bool|array)\((.*)\)$`)

func parsePipelineInput(s string) (string, gitlab.PipelineInputValueInterface, error) {
	f := strings.SplitN(s, ":", 2)
	if len(f) != 2 {
		return "", nil, fmt.Errorf("invalid input %q: want \"key:value\"", s)
	}

	key, strValue := f[0], f[1]
	if key == "" {
		return "", nil, fmt.Errorf("key cannot be empty for input %q", s)
	}

	ms := typedInputValueRE.FindStringSubmatch(strValue)
	switch {
	case len(ms) < 3:
		return key, gitlab.NewPipelineInputValue(strValue), nil

	case ms[1] == "string":
		return key, gitlab.NewPipelineInputValue(ms[2]), nil

	case ms[1] == "array":
		if ms[2] == "" {
			return key, gitlab.NewPipelineInputValue([]string{}), nil
		}
		ms[2] = strings.TrimSuffix(ms[2], ",")
		return key, gitlab.NewPipelineInputValue(strings.Split(ms[2], ",")), nil

	case ms[1] == "bool":
		v, err := strconv.ParseBool(ms[2])
		if err != nil {
			return "", nil, fmt.Errorf("strconv.ParseBool(%q): %w", ms[2], err)
		}

		return key, gitlab.NewPipelineInputValue(v), nil

	case ms[1] == "int":
		i, err := strconv.Atoi(ms[2])
		if err != nil {
			return "", nil, fmt.Errorf("strconv.Atoi(%q): %w", ms[2], err)
		}

		return key, gitlab.NewPipelineInputValue(i), nil

	case ms[1] == "float":
		f, err := strconv.ParseFloat(ms[2], 64)
		if err != nil {
			return "", nil, fmt.Errorf("strconv.ParseFloat(%q): %w", ms[2], err)
		}

		return key, gitlab.NewPipelineInputValue(f), nil

	default:
		// Unreachable
		return "", nil, fmt.Errorf("unrecognized type %q in %q", ms[1], s)
	}
}
