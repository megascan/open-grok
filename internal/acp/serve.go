package acp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/megascan/open-grok/internal/config"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ServeWS runs ACP over WebSocket at bind (default 127.0.0.1:2419).
// Clients authenticate with Authorization: Bearer or ?server-key=.
func ServeWS(ctx context.Context, bind, secret string, opt Options) error {
	if bind == "" {
		bind = "127.0.0.1:2419"
	}
	if secret == "" {
		secret = strings.ReplaceAll(fmt.Sprint(time.Now().UnixNano()), "-", "")
		fmt.Fprintf(os.Stderr, "entropy agent serve: generated secret %s\n", secret[:12])
		secret = secret[:12]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("server-key")
		}
		if token != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = ServeStdio(r.Context(), Options{
			CWD:    opt.CWD,
			Model:  opt.Model,
			YOLO:   opt.YOLO,
			Bearer: opt.Bearer,
			In:     &wsReader{conn: conn},
			Out:    wsWriter{conn: conn},
		})
	})
	srv := &http.Server{Addr: bind, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "entropy ACP websocket listening on %s\n", ln.Addr())
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type wsReader struct {
	conn *websocket.Conn
	buf  []byte
}

func (r *wsReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		_, data, err := r.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		r.buf = append(data, '\n')
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

type wsWriter struct{ conn *websocket.Conn }

func (w wsWriter) Write(p []byte) (int, error) {
	err := w.conn.WriteMessage(websocket.TextMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

var _ io.Reader = (*wsReader)(nil)
var _ io.Writer = wsWriter{}

// ServeLeader listens on the Entropy leader socket and serves ACP JSON-RPC.
func ServeLeader(ctx context.Context, opt Options, noExit bool) error {
	path := config.LeaderSock()
	_ = os.Remove(path)
	ln, err := listenLeader(path)
	if err != nil {
		return err
	}
	defer ln.Close()
	fmt.Fprintf(os.Stderr, "entropy leader listening on %s\n", path)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	var clients int
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		clients++
		go func(c net.Conn) {
			defer c.Close()
			_ = ServeStdio(ctx, Options{
				CWD:    opt.CWD,
				Model:  opt.Model,
				YOLO:   opt.YOLO,
				Bearer: opt.Bearer,
				In:     c,
				Out:    c,
			})
			clients--
			if !noExit && clients <= 0 {
				_ = ln.Close()
			}
		}(conn)
	}
}
