package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	jsonPretty "github.com/tidwall/pretty"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io *iostreams.IOStreams

	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)
	branch    func() (string, error)

	hostname            string
	requestMethod       string
	requestMethodPassed bool
	requestPath         string
	requestInputFile    string
	magicFields         []string
	rawFields           []string
	formFields          []string
	requestHeaders      []string
	showResponseHeaders bool
	paginate            bool
	silent              bool
	outputFormat        string
}

func NewCmdApi(f cmdutils.Factory, runF func(*options) error) *cobra.Command {
	opts := options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
		branch:    f.Branch,
	}

	cmd := &cobra.Command{
		Use:   "api <endpoint>",
		Short: "Make an authenticated request to the GitLab API.",
		Long: heredoc.Docf(`
		Makes an authenticated HTTP request to the GitLab API, and prints the response.
		Specify either a path to a GitLab API v4 endpoint, or %[1]sgraphql%[1]s to access the
		GitLab GraphQL API.

		For more information, see:

		- [GitLab REST API documentation](https://docs.gitlab.com/api/)
		- [GitLab GraphQL documentation](https://docs.gitlab.com/api/graphql/)

		If the current directory is a Git directory, this command uses the GitLab
		authenticated host in the current directory. Otherwise, %[1]sgitlab.com%[1]s is used.
		To override the GitLab hostname, use %[1]s--hostname%[1]s.

		When used in the endpoint argument, these placeholder values are replaced
		with values from the repository of the current directory:

		- %[1]s:branch%[1]s
		- %[1]s:fullpath%[1]s
		- %[1]s:group%[1]s
		- %[1]s:id%[1]s
		- %[1]s:namespace%[1]s
		- %[1]s:repo%[1]s
		- %[1]s:user%[1]s
		- %[1]s:username%[1]s

		The default HTTP request method is %[1]sGET%[1]s when no parameters are added,
		and %[1]sPOST%[1]s otherwise. To override the method, use %[1]s--method%[1]s.

		Pass one or more %[1]s--raw-field%[1]s values in %[1]skey=value%[1]s format to add
		JSON-encoded string parameters to the %[1]sPOST%[1]s body.

		The %[1]s--field%[1]s flag behaves like %[1]s--raw-field%[1]s but converts values
		based on their format:

		- Literal values %[1]strue%[1]s, %[1]sfalse%[1]s, %[1]snull%[1]s, and integer numbers are converted to
		  the matching JSON types.
		- Placeholder values %[1]s:namespace%[1]s, %[1]s:repo%[1]s, and %[1]s:branch%[1]s are populated with values
		  from the repository of the current directory.
		- If the value starts with %[1]s@%[1]s, the rest of the value is interpreted as a
		  filename to read the value from. Pass %[1]s-%[1]s to read from standard input.

		Neither %[1]s--field%[1]s nor %[1]s--raw-field%[1]s parses JSON arrays or objects;
		those values are sent as strings. To pass a JSON body literally, use %[1]s--input%[1]s.

		For GraphQL requests, all fields other than %[1]squery%[1]s and %[1]soperationName%[1]s are
		interpreted as GraphQL variables.

		To send data as %[1]smultipart/form-data%[1]s instead of JSON, use %[1]s--form%[1]s. This is
		required for API endpoints that accept file uploads, such as wiki attachments.
		Pass one or more %[1]s--form%[1]s values in %[1]skey=value%[1]s format. To upload a file,
		prefix the value with %[1]s@%[1]s followed by the file path. Pass %[1]s-%[1]s to read from
		standard input. Do not combine %[1]s--form%[1]s with %[1]s--field%[1]s, %[1]s--raw-field%[1]s, or %[1]s--input%[1]s.

		To pass a raw request body, use %[1]s--input%[1]s with a file path. Pass %[1]s-%[1]s to
		read from standard input. In this mode, parameters specified with %[1]s--field%[1]s
		flags are serialized into URL query parameters.

		In %[1]s--paginate%[1]s mode, all pages of results are requested sequentially until
		no more pages of results remain. For GraphQL requests:

		- The original query must accept an %[1]s$endCursor: String%[1]s variable.
		- The query must fetch the %[1]spageInfo{ hasNextPage, endCursor }%[1]s set of fields from a collection.

		The %[1]s--output%[1]s flag controls the output format:

		- %[1]sjson%[1]s (default): Pretty-printed JSON. Arrays are output as a single JSON array.
		- %[1]sndjson%[1]s: Newline-delimited JSON, also known as JSON Lines. Each array element
		  or object is output on a separate line. This format is more memory-efficient for
		  large datasets and works well with tools like %[1]sjq%[1]s.

		For ndjson format specifications, see the
		[ndjson spec](https://github.com/ndjson/ndjson-spec) and [JSON Lines](https://jsonlines.org/).
		`, "`"),
		Example: heredoc.Doc(`
			# List releases for the current project, expanding the :fullpath placeholder
			glab api projects/:fullpath/releases

			# List issues for a project by URL-encoded path
			glab api projects/gitlab-com%2Fwww-gitlab-com/issues

			# Upload a file to a project wiki
			glab api --method POST projects/:fullpath/wikis/attachments --form "file=@./image.png" --form "branch=main"

			# Debug the HTTP request and response, including headers and body.
			# Use --input (here, piped from stdin) for JSON arrays or objects;
			# --field only converts scalars (bool, int, null, placeholders, @file).
			echo '{"allowed_to_push":[{"user_id":1}]}' | GLAB_DEBUG_HTTP=1 glab api -X PATCH "projects/:fullpath/protected_branches/main" --input -

			# Fetch all pages of issues
			glab api issues --paginate

			# Fetch all pages of issues as newline-delimited JSON
			glab api issues --paginate --output ndjson

			# Pipe paginated output to jq to filter open issues
			glab api issues --paginate --output ndjson | jq 'select(.state == "opened")'

			# Run a simple GraphQL query
			glab api graphql -f query="query { currentUser { username } }"

			# Run a multi-line GraphQL query for project metadata
			glab api graphql -f query='
			  query {
			    project(fullPath: "gitlab-org/gitlab-docs") {
			      name
			      forksCount
			      statistics {
			        wikiSize
			      }
			      issuesEnabled
			      boards {
			        nodes {
			          id
			          name
			        }
			      }
			    }
			  }
			'

			# Run a paginated GraphQL query using an endCursor variable
			glab api graphql --paginate -f query='
			  query($endCursor: String) {
			    project(fullPath: "gitlab-org/graphql-sandbox") {
			      name
			      issues(first: 2, after: $endCursor) {
			        edges {
			          node {
			            title
			          }
			        }
			        pageInfo {
			          endCursor
			          hasNextPage
			        }
			      }
			    }
			  }'
		`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
			"help:environment": heredoc.Doc(`
				GITLAB_TOKEN, OAUTH_TOKEN (in order of precedence): an authentication token for API requests.
				GITLAB_HOST, GITLAB_URI, GITLAB_URL: specify a GitLab host to make request to.
				GLAB_DEBUG_HTTP: print the full HTTP request and response, including headers and body, to stderr.
			`),
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(cmd, args)

			if err := opts.validate(cmd); err != nil {
				return err
			}

			if runF != nil {
				return runF(&opts)
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&opts.hostname, "hostname", "", "The GitLab hostname for the request. Defaults to gitlab.com, or the authenticated host in the current Git directory.")
	fl.StringVarP(&opts.requestMethod, "method", "X", "GET", "The HTTP method for the request.")
	fl.StringArrayVarP(&opts.magicFields, "field", "F", nil, "Add a parameter of inferred type. Using this flag changes the default HTTP method to POST.")
	fl.StringArrayVarP(&opts.rawFields, "raw-field", "f", nil, "Add a string parameter.")
	fl.StringArrayVar(&opts.formFields, "form", nil, "Add a multipart form field. To upload a file, prefix the value with @ followed by the file path. To read from standard input, use @- (at most once). Using this flag changes the default HTTP method to POST.")
	fl.StringArrayVarP(&opts.requestHeaders, "header", "H", nil, "Add an additional HTTP request header.")
	fl.BoolVarP(&opts.showResponseHeaders, "include", "i", false, "Include HTTP response headers in the output.")
	fl.BoolVar(&opts.paginate, "paginate", false, "Make additional HTTP requests to fetch all pages of results.")
	fl.StringVar(&opts.requestInputFile, "input", "", "The file to use as the body for the HTTP request.")
	fl.BoolVar(&opts.silent, "silent", false, "Do not print the response body.")
	fl.Var(cmdutils.NewEnumValue([]string{"json", "ndjson"}, "json", &opts.outputFormat), "output", "Format output as: json, ndjson.")
	cmd.MarkFlagsMutuallyExclusive("paginate", "input")
	cmd.MarkFlagsMutuallyExclusive("form", "field")
	cmd.MarkFlagsMutuallyExclusive("form", "raw-field")
	cmd.MarkFlagsMutuallyExclusive("form", "input")
	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) {
	o.requestPath = args[0]
	o.requestMethodPassed = cmd.Flags().Changed("method")
}

func (o *options) validate(cmd *cobra.Command) error {
	if cmd.Flags().Changed("hostname") {
		if err := glinstance.HostnameValidator(o.hostname); err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --hostname: %w", err)}
		}
	}

	if o.paginate && !strings.EqualFold(o.requestMethod, http.MethodGet) && o.requestPath != "graphql" {
		return &cmdutils.FlagError{Err: errors.New("the '--paginate' option is not supported for non-GET requests")}
	}

	if o.outputFormat != "json" && o.outputFormat != "ndjson" {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid output format %q: must be 'json' or 'ndjson'", o.outputFormat)}
	}

	stdinCount := 0
	for _, f := range o.formFields {
		if strings.HasSuffix(f, "=@-") {
			stdinCount++
		}
	}
	if stdinCount > 1 {
		return &cmdutils.FlagError{Err: errors.New("'@-' (stdin) can only be used once across all --form fields")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	params, err := parseFields(o)
	if err != nil {
		return err
	}
	isGraphQL := o.requestPath == "graphql"
	requestPath, err := fillPlaceholders(o.requestPath, o, true)
	if err != nil {
		return fmt.Errorf("unable to expand placeholder in path: %w", err)
	}
	method := o.requestMethod
	requestHeaders := o.requestHeaders
	var requestBody any = params

	if !o.requestMethodPassed && (len(params) > 0 || o.requestInputFile != "") {
		method = http.MethodPost
	}

	if o.paginate && !isGraphQL {
		requestPath = addPerPage(requestPath, 100, params)
	}

	if o.requestInputFile != "" {
		file, size, err := openUserFile(o.requestInputFile, o.io.In)
		if err != nil {
			return err
		}
		defer file.Close()
		requestPath, err = parseQuery(requestPath, params)
		if err != nil {
			return err
		}
		requestBody = file
		if size >= 0 {
			requestHeaders = append([]string{fmt.Sprintf("Content-Length: %d", size)}, requestHeaders...)
		}
	} else if len(o.formFields) > 0 {
		if !o.requestMethodPassed {
			method = http.MethodPost
		}
		body, contentType := buildMultipartBody(o.formFields, o.io.In)
		requestBody = body
		requestHeaders = append([]string{fmt.Sprintf("Content-Type: %s", contentType)}, requestHeaders...)
	}

	headersOutputStream := o.io.StdOut
	if o.silent {
		o.io.StdOut = io.Discard
	} else {
		err := o.io.StartPager()
		if err != nil {
			return err
		}
		defer o.io.StopPager()
	}

	var repoHost string
	if o.hostname != "" {
		repoHost = o.hostname
	} else {
		// NOTE: check if repository is available
		br, err := o.baseRepo()
		if err == nil {
			repoHost = br.RepoHost()
		}
	}
	client, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}

	hasNextPage := true
	for hasNextPage {
		resp, err := httpRequest(ctx, client, method, requestPath, requestBody, requestHeaders)
		if err != nil {
			return err
		}

		endCursor, err := processResponse(resp, o, headersOutputStream)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}

		if !o.paginate {
			break
		}

		if isGraphQL {
			hasNextPage = endCursor != ""
			if hasNextPage {
				params["endCursor"] = endCursor
			}
		} else {
			requestPath, hasNextPage = findNextPage(resp)
		}

		if hasNextPage && o.showResponseHeaders {
			o.io.LogInfo()
		}
	}

	return nil
}

func processResponse(resp *http.Response, opts *options, headersOutputStream io.Writer) (string, error) {
	if opts.showResponseHeaders {
		fmt.Fprintln(headersOutputStream, resp.Proto, resp.Status) //nolint:forbidigo // headersOutputStream is a generic io.Writer, passed io.Discard in tests
		printHeaders(headersOutputStream, resp.Header, opts.io.ColorEnabled())
		fmt.Fprint(headersOutputStream, "\r\n") //nolint:forbidigo // headersOutputStream is a generic io.Writer, passed io.Discard in tests
	}

	if resp.StatusCode == http.StatusNoContent {
		return "", nil
	}
	var responseBody io.Reader = resp.Body

	isJSON, _ := regexp.MatchString(`[/+]json(;|$)`, resp.Header.Get("Content-Type"))

	var serverError string
	if isJSON && (opts.requestPath == "graphql" || resp.StatusCode >= http.StatusBadRequest) {
		var err error
		responseBody, serverError, err = parseErrorResponse(responseBody, resp.StatusCode)
		if err != nil {
			return "", err
		}
	}

	var bodyCopy *bytes.Buffer
	isGraphQLPaginate := isJSON && resp.StatusCode == http.StatusOK && opts.paginate && opts.requestPath == "graphql"
	if isGraphQLPaginate {
		bodyCopy = &bytes.Buffer{}
		responseBody = io.TeeReader(responseBody, bodyCopy)
	}

	var err error
	// Handle NDJSON output format
	if opts.outputFormat == "ndjson" && isJSON && resp.StatusCode == http.StatusOK {
		err = streamNDJSON(responseBody, opts.io.StdOut)
	} else if isJSON && opts.io.ColorEnabled() {
		out := &bytes.Buffer{}
		_, err = io.Copy(out, responseBody)
		if err == nil {
			result := jsonPretty.Color(jsonPretty.Pretty(out.Bytes()), nil)
			_, err = fmt.Fprintln(opts.io.StdOut, string(result)) //nolint:forbidigo // write error must propagate to the caller
		}
	} else {
		_, err = io.Copy(opts.io.StdOut, responseBody)
	}
	if err != nil {
		return "", err
	}

	if serverError != "" {
		opts.io.LogErrorf("glab: %s\n", serverError)
		return "", cmdutils.SilentError
	} else if resp.StatusCode > 299 {
		opts.io.LogErrorf("glab: HTTP %d\n", resp.StatusCode)
		return "", cmdutils.SilentError
	}

	if isGraphQLPaginate {
		return findEndCursor(bodyCopy), nil
	}

	return "", nil
}

// streamNDJSON streams JSON response as newline-delimited JSON.
// If the response is a JSON array, each element is written as a separate line.
// If the response is a single JSON object, it's written as-is with a newline.
func streamNDJSON(body io.Reader, out io.Writer) error {
	dec := json.NewDecoder(body)
	dec.UseNumber()

	// Peek at the first token to determine if it's an array or object
	token, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}

	// Check if it's an array
	if delim, ok := token.(json.Delim); ok && delim == '[' {
		// Stream each array element as a separate line
		for dec.More() {
			var element json.RawMessage
			if err := dec.Decode(&element); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, string(element)); err != nil { //nolint:forbidigo // out is a generic io.Writer parameter for streaming, not IOStreams directly
				return err
			}
		}
		// Consume the closing bracket
		if _, err := dec.Token(); err != nil {
			return err
		}
		return nil
	}

	// For non-array JSON, simply copy the entire body and add newline
	// Reset the body reader since we consumed the first token
	var tokenStr string
	switch v := token.(type) {
	case json.Delim:
		tokenStr = string(rune(v))
	default:
		// For primitive values (string, number, bool, null), marshal back to JSON
		b, _ := json.Marshal(v) //nolint:forbidigo // re-encoding a parsed JSON token value into a string, not writing to stdout
		tokenStr = string(b)
	}

	fullBody := io.MultiReader(strings.NewReader(tokenStr), dec.Buffered(), body)

	// Copy directly to output with newline
	if _, err := io.Copy(out, fullBody); err != nil {
		return err
	}
	_, err = out.Write([]byte("\n"))
	return err
}

var placeholderRE = regexp.MustCompile(`:(group/:namespace/:repo|namespace/:repo|fullpath|id|user|username|group|namespace|repo|branch)\b`)

// fillPlaceholders populates `:namespace` and `:repo` placeholders with values from the current repository.
// When escapePath is true, substituted values are URL-encoded so they're safe as a single path segment;
// callers expanding placeholders into request bodies or query values should pass false to preserve raw values.
func fillPlaceholders(value string, opts *options, escapePath bool) (string, error) {
	if !placeholderRE.MatchString(value) {
		return value, nil
	}

	maybeEscape := func(s string) string {
		if escapePath {
			return url.PathEscape(s)
		}
		return s
	}

	var err error
	filled := placeholderRE.ReplaceAllStringFunc(value, func(m string) string {
		switch m {
		case ":id":
			baseRepo, baseRepoErr := opts.baseRepo()
			if baseRepoErr != nil {
				err = baseRepoErr
				return ""
			}

			h, _ := opts.apiClient(baseRepo.RepoHost())
			project, e := baseRepo.Project(h.Lab())
			if e == nil && project != nil {
				return strconv.FormatInt(project.ID, 10)
			}
			err = e
			return ""
		case ":group/:namespace/:repo", ":fullpath":
			baseRepo, baseRepoErr := opts.baseRepo()
			if baseRepoErr != nil {
				err = baseRepoErr
				return ""
			}
			return maybeEscape(baseRepo.FullName())
		case ":namespace/:repo":
			baseRepo, baseRepoErr := opts.baseRepo()
			if baseRepoErr != nil {
				err = baseRepoErr
				return ""
			}

			return maybeEscape(baseRepo.RepoNamespace() + "/" + baseRepo.RepoName())
		case ":group":
			baseRepo, baseRepoErr := opts.baseRepo()
			if baseRepoErr != nil {
				err = baseRepoErr
				return ""
			}

			return maybeEscape(baseRepo.RepoGroup())
		case ":user", ":username":
			h, _ := opts.apiClient("")
			u, _, e := h.Lab().Users.CurrentUser()
			if e == nil && u != nil {
				return maybeEscape(u.Username)
			}
			err = e
			return m
		case ":namespace":
			baseRepo, baseRepoErr := opts.baseRepo()
			if baseRepoErr != nil {
				err = baseRepoErr
				return ""
			}

			return maybeEscape(baseRepo.RepoNamespace())
		case ":repo":
			baseRepo, baseRepoErr := opts.baseRepo()
			if baseRepoErr != nil {
				err = baseRepoErr
				return ""
			}

			return maybeEscape(baseRepo.RepoName())
		case ":branch":
			branch, e := opts.branch()
			if e != nil {
				err = e
			}
			return maybeEscape(branch)
		default:
			err = fmt.Errorf("invalid placeholder: %q", m)
			return ""
		}
	})

	if err != nil {
		return value, err
	}

	return filled, nil
}

func printHeaders(w io.Writer, headers http.Header, colorize bool) {
	var names []string
	for name := range headers {
		if name == "Status" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var headerColor, headerColorReset string
	if colorize {
		headerColor = "\x1b[1;34m" // bright blue
		headerColorReset = "\x1b[m"
	}
	for _, name := range names {
		fmt.Fprintf(w, "%s%s%s: %s\r\n", headerColor, name, headerColorReset, strings.Join(headers[name], ", ")) //nolint:forbidigo // w is a generic io.Writer, passed io.Discard in tests
	}
}

func parseFields(opts *options) (map[string]any, error) {
	params := make(map[string]any)
	for _, f := range opts.rawFields {
		key, value, err := parseField(f)
		if err != nil {
			return params, err
		}
		params[key] = value
	}
	for _, f := range opts.magicFields {
		key, strValue, err := parseField(f)
		if err != nil {
			return params, err
		}
		value, err := magicFieldValue(strValue, opts)
		if err != nil {
			return params, fmt.Errorf("error parsing %q value: %w", key, err)
		}
		params[key] = value
	}
	return params, nil
}

func parseField(f string) (string, string, error) {
	idx := strings.IndexRune(f, '=')
	if idx == -1 {
		return f, "", fmt.Errorf("field %q requires a value separated by an '=' sign", f)
	}
	return f[0:idx], f[idx+1:], nil
}

func magicFieldValue(v string, opts *options) (any, error) {
	if strings.HasPrefix(v, "@") {
		return readUserFile(v[1:], opts.io.In)
	}

	if n, err := strconv.Atoi(v); err == nil {
		return n, nil
	}

	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	default:
		return fillPlaceholders(v, opts, false)
	}
}

func readUserFile(fn string, stdin io.ReadCloser) ([]byte, error) {
	var r io.ReadCloser
	if fn == "-" {
		r = stdin
	} else {
		var err error
		r, err = os.Open(fn)
		if err != nil {
			return nil, err
		}
	}
	defer r.Close()
	return io.ReadAll(r)
}

func openUserFile(fn string, stdin io.ReadCloser) (io.ReadCloser, int64, error) {
	if fn == "-" {
		return stdin, -1, nil
	}

	r, err := os.Open(fn)
	if err != nil {
		return r, -1, err
	}

	s, err := os.Stat(fn)
	if err != nil {
		r.Close()
		return r, -1, err
	}

	return r, s.Size(), nil
}

func parseErrorResponse(r io.Reader, statusCode int) (io.Reader, string, error) {
	bodyCopy := &bytes.Buffer{}
	b, err := io.ReadAll(io.TeeReader(r, bodyCopy))
	if err != nil {
		return r, "", err
	}

	var parsedBody struct {
		Message string
		Errors  []json.RawMessage
	}
	err = json.Unmarshal(b, &parsedBody)
	if err != nil {
		// in cases where it's an object within an object we can try to parse it as is
		var t any
		err = json.Unmarshal(b, &t)
		if err != nil {
			return r, "", err
		}
		return bodyCopy, fmt.Sprintf("%v+", t), nil
	}
	if parsedBody.Message != "" {
		return bodyCopy, fmt.Sprintf("%s (HTTP %d)", parsedBody.Message, statusCode), nil
	}

	type errorMessage struct {
		Message string
	}
	var respErrors []string
	for _, rawErr := range parsedBody.Errors {
		if len(rawErr) == 0 {
			continue
		}
		if rawErr[0] == '{' {
			var objectError errorMessage
			err := json.Unmarshal(rawErr, &objectError)
			if err != nil {
				return r, "", err
			}
			respErrors = append(respErrors, objectError.Message)
		} else if rawErr[0] == '"' {
			var stringError string
			err := json.Unmarshal(rawErr, &stringError)
			if err != nil {
				return r, "", err
			}
			respErrors = append(respErrors, stringError)
		}
	}

	if len(respErrors) > 0 {
		return bodyCopy, strings.Join(respErrors, "\n"), nil
	}

	return bodyCopy, "", nil
}
