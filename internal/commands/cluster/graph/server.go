package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"

	"github.com/coder/websocket"
	"github.com/pkg/browser"
	"oss.terrastruct.com/d2/d2format"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/lib/log"
	"oss.terrastruct.com/d2/lib/textmeasure"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	graphAPISubprotocolName = "gitlab-agent-graph-api"
)

type server struct {
	log                   *slog.Logger
	io                    *iostreams.IOStreams
	httpClient            *http.Client
	graphAPIURL           string
	listenNet, listenAddr string
	authorization         string
	watchRequest          []byte
}

func (s *server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, s.listenNet, s.listenAddr)
	if err != nil {
		return err
	}
	s.io.LogInfof("Listening on http://%s\n", l.Addr())
	srv := &http.Server{
		Handler: http.HandlerFunc(s.handle),
	}
	err = browser.OpenURL(fmt.Sprintf("http://%s", l.Addr()))
	if err != nil {
		s.io.LogError("Failed to open browser:", err)
	}
	err = srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, s.graphAPIURL, &websocket.DialOptions{ //nolint:bodyclose // upgrade response body is consumed by the websocket library
		HTTPClient: s.httpClient,
		HTTPHeader: http.Header{
			"Authorization": []string{s.authorization},
		},
		Subprotocols: []string{graphAPISubprotocolName},
	})
	if err != nil {
		s.reportError(w, fmt.Errorf("WebSocket dial: %w", err), false)
		return
	}
	conn.SetReadLimit(4 * 1024 * 1024) // 1MiB is Kubernetes max object size (that'd be the minimum).

	err = conn.Write(ctx, websocket.MessageText, s.watchRequest)
	if err != nil {
		s.reportError(w, fmt.Errorf("WebSocket write: %w", err), false)
		return
	}
	ctx = log.With(ctx, s.log) // logger for D2
	graphSrcC, readyC := s.buildGraph(ctx, s.readActions(ctx, conn))
	s.renderAndWrite(ctx, w, graphSrcC, readyC)
}

func (s *server) readActions(ctx context.Context, conn *websocket.Conn) <-chan dataError[jsonWatchGraphResponse] {
	resp := make(chan dataError[jsonWatchGraphResponse])
	go func() {
		done := ctx.Done()
		for {
			mt, data, err := conn.Read(ctx)
			if err != nil {
				sendErr(done, resp, fmt.Errorf("WebSocket read: %w", err))
				return
			}
			if mt != websocket.MessageText { // shouldn't ever happen
				sendErr(done, resp, errors.New("unexpected message type"))
				return
			}
			var d jsonWatchGraphResponse
			err = json.Unmarshal(data, &d)
			if err != nil {
				sendErr(done, resp, fmt.Errorf("JSON unmarshal: %w", err))
				return
			}
			sendData(done, resp, d)
		}
	}()
	return resp
}

func (s *server) buildGraph(ctx context.Context, srcC <-chan dataError[jsonWatchGraphResponse]) (<-chan dataError[string], chan<- struct{}) {
	resp := make(chan dataError[string], 1) // enable non-blocking send
	readyC := make(chan struct{}, 1)        // enable non-blocking send
	go func() {
		done := ctx.Done()
		b, err := newGraphBuilder(ctx, s.io)
		if err != nil {
			sendErr(done, resp, fmt.Errorf("newGraphBuilder: %w", err))
			return
		}
		producerReady := false
		consumerReady := false
		lastSentAST := ""
		for {
			select {
			case <-done:
				return
			case <-readyC:
				consumerReady = true
			case src := <-srcC:
				if src.err != nil {
					sendErr(done, resp, src.err)
					return
				}
				s.logWarnings(src.data.Warnings)
				if src.data.Error != nil {
					sendErr(done, resp, src.data.Error)
					return
				}
				err = b.applyActions(src.data.Actions)
				if err != nil {
					sendErr(done, resp, err)
					return
				}
				producerReady = true
			}
			if consumerReady && producerReady {
				newAST := d2format.Format(b.g.AST)
				if newAST == lastSentAST {
					continue // no changes, skip sending.
				}
				resp <- dataError[string]{data: newAST}
				consumerReady = false
				producerReady = false
				lastSentAST = newAST
			}
		}
	}()
	return resp, readyC
}

// must run on the http server's handler goroutine since can panic.
func (s *server) renderAndWrite(ctx context.Context, w http.ResponseWriter, srcC <-chan dataError[string], readyC chan<- struct{}) {
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		s.reportError(w, fmt.Errorf("textmeasure.NewRuler: %w", err), false)
		return
	}
	compileOpts := &d2lib.CompileOptions{
		Ruler: ruler,
		LayoutResolver: func(engine string) (d2graph.LayoutGraph, error) {
			return d2dagrelayout.DefaultLayout, nil
		},
	}
	renderOpts := &d2svg.RenderOpts{}
	flush := http.NewResponseController(w).Flush
	header := textproto.MIMEHeader{
		"Content-Type": []string{"image/svg+xml"},
	}
	mw := multipart.NewWriter(w)
	done := ctx.Done()
	var part io.Writer
	nilableReadyC := readyC
	for {
		select {
		case <-done:
			panic(http.ErrAbortHandler)
		case nilableReadyC <- struct{}{}: // let the producer know we are ready to receive the next frame
			nilableReadyC = nil // ok, producer knows we are ready, we can stop poking it for now.
		case src := <-srcC:
			nilableReadyC = readyC // re-enable the above case
			if src.err != nil {
				s.reportError(w, src.err, part != nil)
				return
			}
			diagram, _, err := d2lib.Compile(
				ctx,
				src.data,
				compileOpts,
				renderOpts,
			)
			if err != nil {
				s.reportError(w, fmt.Errorf("d2lib.Compile: %w", err), part != nil)
				return
			}

			svgData, err := d2svg.Render(diagram, renderOpts)
			if err != nil {
				s.reportError(w, fmt.Errorf("d2svg.Render: %w", err), part != nil)
				return
			}
			if part == nil { // first part
				h := w.Header()
				h.Set("Content-Type", "multipart/x-mixed-replace;boundary="+mw.Boundary())
				h.Set("Transfer-Encoding", "identity")
				w.WriteHeader(http.StatusOK)
				part, err = mw.CreatePart(header)
				if err != nil {
					s.reportError(w, fmt.Errorf("CreatePart: %w", err), true)
					return
				}
			}

			_, err = part.Write(svgData)
			if err != nil {
				s.reportError(w, err, true)
				return
			}
			part, err = mw.CreatePart(header)
			if err != nil {
				s.reportError(w, fmt.Errorf("CreatePart: %w", err), true)
				return
			}
			err = flush()
			if err != nil {
				s.reportError(w, err, true)
				return
			}
		}
	}
}

func (s *server) logWarnings(w []jsonWatchGraphWarning) {
	for _, warning := range w {
		if len(warning.Attributes) > 0 {
			s.io.LogInfof("Warning: %s: %s (%v)\n", warning.Type, warning.Message, warning.Attributes)
		} else {
			s.io.LogInfof("Warning: %s: %s\n", warning.Type, warning.Message)
		}
	}
}

func (s *server) reportError(w http.ResponseWriter, err error, dataWritten bool) {
	s.io.LogError(err.Error())
	if dataWritten {
		// we've written something already, the only way to let the caller know there was an issue is to
		// drop the connection.
		panic(http.ErrAbortHandler)
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type dataError[T any] struct {
	data T
	err  error
}

func sendErr[T any](done <-chan struct{}, resp chan<- dataError[T], err error) {
	sendDataOrErr(done, resp, dataError[T]{err: err})
}

func sendData[T any](done <-chan struct{}, resp chan<- dataError[T], data T) {
	sendDataOrErr(done, resp, dataError[T]{data: data})
}

func sendDataOrErr[T any](done <-chan struct{}, resp chan<- dataError[T], v dataError[T]) {
	select {
	case <-done:
	case resp <- v:
	}
}
