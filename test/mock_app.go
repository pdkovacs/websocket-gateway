package test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	wsgw "websocket-gateway/internal"

	"github.com/gin-gonic/gin"
)

const badCredential = "bad-credential"

type mockApplication struct {
	wsgwUrl      string
	listener     net.Listener
	stop         func()
	dataReceived [][]string
}

func newMockApp(wsgwUrl string) *mockApplication {
	return &mockApplication{
		wsgwUrl: wsgwUrl,
	}
}

func (m *mockApplication) start() error {
	address := fmt.Sprintf(":%d", 0)
	listener, listenErr := net.Listen("tcp", address)
	if listenErr != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, listenErr)
	}
	m.listener = listener

	handler, creHandlerErr := m.createMockAppRequestHandler()
	if creHandlerErr != nil {
		return fmt.Errorf("failed to create mockApp request handler: %w", creHandlerErr)
	}

	go func() {
		http.Serve(listener, handler)
	}()
	m.stop = func() {
		listener.Close()
	}

	return nil
}

func (m *mockApplication) createMockAppRequestHandler() (http.Handler, error) {
	rootEngine := gin.Default()
	rootEngine.Use(wsgw.RequestLogger)
	ws := rootEngine.Group("/ws")

	ws.POST("/connecting", func(g *gin.Context) {
		req := g.Request
		res := g
		cred, hasCredHeader := req.Header["Authorization"]
		if !hasCredHeader {
			res.AbortWithError(500, errors.New("authorization header not found"))
			return
		}
		if cred[0] == badCredential {
			res.AbortWithError(401, errors.New("bad credentials in Authorization header"))
		}

		connHeaderKey := wsgw.ConnectionIDHeaderKey
		if connId := req.Header.Get(connHeaderKey); connId != "" {
			m.dataReceived = [][]string{{"POST /ws/connecting", connHeaderKey, connId}}
		}

		res.Status(200)
	})

	ws.POST("/disconnected", func(g *gin.Context) {
		req := g.Request

		connHeaderKey := wsgw.ConnectionIDHeaderKey
		if connId := req.Header.Get(connHeaderKey); connId != "" {
			m.dataReceived = append(m.dataReceived, []string{"POST /ws/disconnected", connHeaderKey, connId})
		}
	})

	ws.POST("/message-received")

	return rootEngine, nil
}
