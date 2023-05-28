package test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	wsgw "websocket-gateway/internal"
	"websocket-gateway/internal/logging"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/suite"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const wsgwPort = 8080

type connectingTestSuite struct {
	suite.Suite
	mockApp   *mockApplication
	wsGateway *wsgw.Server
	logger    zerolog.Logger
}

func TestConnectingTestSuite(t *testing.T) {
	suite.Run(t, &connectingTestSuite{
		logger: logging.Get().With().Str("unit", "TestConnectingTestSuite").Logger(),
	})
}

func (s *connectingTestSuite) SetupSuite() {
	logger := s.logger.With().Str(logging.MethodLogger, "SetupSuite").Logger()
	logger.Info().Msg("BEGIN")

	s.mockApp = newMockApp(
		fmt.Sprintf("http://localhost:%d", wsgwPort),
	)

	mockAppStartErr := s.mockApp.start()
	if mockAppStartErr != nil {
		panic(mockAppStartErr)
	}

	server := wsgw.CreateServer(
		wsgw.Config{
			ServerHost:          "localhost",
			ServerPort:          wsgwPort,
			AppBaseUrl:          fmt.Sprintf("http://%s", s.mockApp.listener.Addr().String()),
			LoadBalancerAddress: "",
		},
		s.logger.With().Str(logging.ServiceLogger, "websocket-gateway").Logger(),
	)
	s.wsGateway = server

	var wg sync.WaitGroup
	wg.Add(1)
	go server.SetupAndStart(func(port int, stop func()) {
		fmt.Fprint(os.Stderr, "WsGateway is ready!")
		wg.Done()
	})
	wg.Wait()
}

func (s *connectingTestSuite) TearDownSuite() {
	if s.mockApp != nil {
		s.mockApp.stop()
	}
	if s.wsGateway != nil {
		s.wsGateway.Stop()
	}
}

func (s *connectingTestSuite) GetReceivedConnectionId(callIndex int) string {
	testDataReceived := s.mockApp.dataReceived
	if len(testDataReceived) <= callIndex {
		return ""
	}
	s.Equal(strings.ToUpper(testDataReceived[callIndex][1]), strings.ToUpper((wsgw.ConnectionIDHeaderKey)))
	return testDataReceived[callIndex][2]
}

func (s *connectingTestSuite) TestHappyConnecting() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	c, _, err := connectToWs(ctx, wsgwPort, defaultDialOptions)
	s.NoError(err)
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "we're done")

	err = wsjson.Write(ctx, c, "hi")
	s.NoError(err)
	s.Greater(len(s.GetReceivedConnectionId(0)), 19)
}

func (s *connectingTestSuite) TestConnectingWithBadCredentials() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, response, _ := connectToWs(ctx, wsgwPort, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{badCredential},
		},
	})
	s.Equal(response.StatusCode, 401)
}

func (s *connectingTestSuite) TestDisconnection() {
	logger := s.logger.With().Str(logging.MethodLogger, "TestDisconnection").Logger()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	c, _, err := connectToWs(ctx, wsgwPort, defaultDialOptions)
	s.NoError(err)
	if err != nil {
		return
	}

	connId := s.GetReceivedConnectionId(0)

	logger.Debug().Msg("about to close websocket...")
	c.Close(websocket.StatusNormalClosure, "we're done")
	logger.Debug().Msg("websocket closed")

	start := time.Now()
	probeTimer := time.NewTimer(10 * time.Millisecond)
	waitTimer := time.NewTimer(5 * time.Second)
	for {
		select {
		case <-probeTimer.C:
			connId1 := s.GetReceivedConnectionId(1)
			if connId1 != connId {
				continue
			}
			logger.Debug().Dur("elapsed_ms", time.Since(start)).Msg(">>>>>")
			s.Equal(connId, connId1)
			return
		case <-waitTimer.C:
			connId1 := s.GetReceivedConnectionId(1)
			logger.Debug().Dur("elapsed_ms", time.Since(start)).Msg("timed out")
			s.Equal(connId, connId1)
		}
	}
}

var defaultDialOptions = &websocket.DialOptions{
	HTTPHeader: http.Header{
		"Authorization": []string{"some credentials"},
	},
}

func connectToWs(ctx context.Context, wsgwPort int, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, fmt.Sprintf("ws://localhost:%d/connect", wsgwPort), options)
}
