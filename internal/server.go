package wsgw

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
	logging "websocket-gateway/internal/logging"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type Config struct {
	ServerHost          string
	ServerPort          int
	AppBaseUrl          string
	LoadBalancerAddress string // TODO: remove this
}

type Server struct {
	Addr          string
	listener      net.Listener
	configuration Config
	logger        zerolog.Logger
}

func CreateServer(configuration Config, logging zerolog.Logger) *Server {
	return &Server{
		configuration: configuration,
		logger:        logging,
	}
}

// start starts the service
func (s *Server) start(r http.Handler, ready func(port int, stop func())) {

	logging := s.logger.With().Str(logging.MethodLogger, "StartServer").Logger()
	logging.Info().Msg("Starting server on ephemeral....")
	var err error

	s.listener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", s.configuration.ServerHost, s.configuration.ServerPort))
	if err != nil {
		panic(fmt.Sprintf("Error while starting to listen at an ephemeral port: %v", err))
	}
	s.Addr = s.listener.Addr().String()
	logging.Info().Str("address", s.Addr).Msg("websocket-gateway instance is listening")

	_, port, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		panic(fmt.Sprintf("Error while parsing the server address: %v", err))
	}

	if ready != nil {
		portAsInt, err := strconv.Atoi(port)
		if err != nil {
			panic(err)
		}
		ready(portAsInt, s.Stop)
	}

	http.Serve(s.listener, r)
}

// SetupAndStart sets up and starts server.
func (s *Server) SetupAndStart(ready func(port int, stop func())) {
	r := createWsGwRequestHandler(s.configuration, s.logger)
	s.start(r, ready)
}

// For now, we assume that the backend authentication is managed ex-machina by the environment (AWS role or K8S NetworkPolicy
// or by a service-mesh provider)
// In the unlikely case of ex-machina control isn't available, OAuth2 client credentials flow could be easily supported.
// (Use https://pkg.go.dev/github.com/golang-jwt/jwt/v4#example-package-GetTokenViaHTTP to verify the token.)
func authenticateBackend(c *gin.Context) error {
	return nil
}

// Calls the `POST /ws/message-received` endpoint on the backend with "msg" and "connectionId"
func onMessageReceived(msg string, connectionId connectionID) error {
	return nil
}

// Stop kills the listener
func (s *Server) Stop() {
	logging := s.logger.With().Str(logging.MethodLogger, "Stop").Logger()
	error := s.listener.Close()
	if error != nil {
		logging.Error().Err(error).Msg("error while closing listener")
	} else {
		logging.Info().Msg("Listener closed successfully")
	}

}

func createWsGwRequestHandler(options Config, logging zerolog.Logger) *gin.Engine {
	rootEngine := gin.Default()

	rootEngine.Use(RequestLogger)

	wsConns := newWsConnections()

	appUrls := appURLs{
		baseUrl: options.AppBaseUrl,
	}

	rootEngine.GET(
		"/connect",
		connectHandler(
			&appUrls,
			wsConns,
			options.LoadBalancerAddress,
			onMessageReceived,
		),
	)

	rootEngine.POST(
		"/message/:connectionId",
		pushHandler(
			authenticateBackend,
			"connectionId",
			wsConns,
		),
	)

	return rootEngine
}

type appURLs struct {
	baseUrl string
}

func (u *appURLs) connecting() string {
	return fmt.Sprintf("%s/ws/connecting", u.baseUrl)
}

func (u *appURLs) disconnected() string {
	return fmt.Sprintf("%s/ws/disconnected", u.baseUrl)
}

func RequestLogger(g *gin.Context) {
	start := time.Now()

	l := logging.Get()

	r := g.Request
	g.Request = r.WithContext(l.WithContext(r.Context()))

	lrw := newLoggingResponseWriter(g.Writer)

	defer func() {
		panicVal := recover()
		if panicVal != nil {
			lrw.statusCode = http.StatusInternalServerError // ensure that the status code is updated
			panic(panicVal)                                 // continue panicking
		}
		l.
			Info().
			Str("method", g.Request.Method).
			Str("url", g.Request.URL.RequestURI()).
			Str("user_agent", g.Request.UserAgent()).
			Int("status_code", lrw.statusCode).
			Dur("elapsed_ms", time.Since(start)).
			Msg("incoming request")
	}()

	g.Next()
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func newLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{w, http.StatusOK}
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}
