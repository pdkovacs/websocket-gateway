package wsgw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"
)

// TODO: make this configurable?
const ConnectionIDHeaderKey = "X-WSGW-CONNECTION-ID"

type wsIOAdapter struct {
	wsConn *websocket.Conn
}

func (wsIo *wsIOAdapter) CloseRead(ctx context.Context) context.Context {
	return wsIo.wsConn.CloseRead(ctx)
}

func (wsIo *wsIOAdapter) Close() error {
	return wsIo.wsConn.Close(websocket.StatusPolicyViolation, "connection too slow to keep up with messages")
}

func (wsIo *wsIOAdapter) Write(ctx context.Context, msg string) error {
	return wsIo.wsConn.Write(ctx, websocket.MessageText, []byte(msg))
}

func (wsIo *wsIOAdapter) Read(ctx context.Context) (string, error) {
	msgType, msg, err := wsIo.wsConn.Read(ctx)
	if err != nil {
		return "", err
	}
	if msgType != websocket.MessageText {
		return "", errors.New("unexpected message type")
	}
	return string(msg), nil
}

type applicationURLs interface {
	connecting() string
	disconnected() string
}

// Relays the connection request to the backend's `POST /ws/connecting` endpoint and
func notifyAppOfWsConnectionChange(notificationUrl string, connId connectionID, g *gin.Context, parentLogger zerolog.Logger) bool {
	logger := parentLogger.With().Str("method", fmt.Sprintf("notifyAppOfWsConnecting: %s", notificationUrl)).Logger()

	logger.Debug().Msg("BEGIN")
	defer logger.Debug().Msg("END")

	logger.Debug().Msg("Creating request...")
	request, err := http.NewRequest(http.MethodPost, notificationUrl, nil)
	if err != nil {
		logger.Error().Stack().Err(err).Msg("failed to create request object")
		g.AbortWithStatus(http.StatusInternalServerError)
		return false
	}
	request.Header = g.Request.Header

	request.Header.Add(ConnectionIDHeaderKey, string(connId))

	// TODO: doesn't recreate the client for each request
	client := http.Client{
		Timeout: time.Second * 15,
	}
	logger.Debug().Msg("executing request...")
	response, requestErr := client.Do(request)
	if requestErr != nil {
		logger.Error().Stack().Err(requestErr).Msg("failed to send request")
		g.AbortWithStatus(http.StatusInternalServerError)
		return false
	}
	logger.Debug().Int("status_code", response.StatusCode).Msg("checking status code...")
	if response.StatusCode == http.StatusUnauthorized {
		logger.Info().Msg("Authentication failed")
		g.AbortWithStatus(http.StatusUnauthorized)
		return false
	}
	if response.StatusCode != 200 {
		logger.Info().Int("status_code", response.StatusCode).Msg("unexpected status code")
		g.AbortWithStatus(http.StatusInternalServerError)
		return false
	}
	return true
}

// connectHandler calls `authenticateClient` if it is not `nil` to authenticate the client,
// then notifies the application of the new WS connection
func connectHandler(
	appUrls applicationURLs,
	ws *wsConnections,
	loadBalancerAddress string,
	onMessageReceived onMgsReceivedFunc,
) gin.HandlerFunc {
	return func(g *gin.Context) {

		logger := zerolog.Ctx(g.Request.Context()).With().Str("client connecting", g.Request.RemoteAddr).Logger()

		connId := createID()

		appAccepted := notifyAppOfWsConnectionChange(appUrls.connecting(), connId, g, logger)
		logger.Debug().Bool("app_accepted", appAccepted)

		if !appAccepted {
			return
		}

		wsConn, subsErr := websocket.Accept(g.Writer, g.Request, &websocket.AcceptOptions{
			OriginPatterns: []string{loadBalancerAddress},
		})
		if subsErr != nil {
			logger.Error().Stack().Err(subsErr).Msg("failed to accept WS connection request")
			g.Error(subsErr)
			g.AbortWithStatus(500)
			return
		}
		defer wsConn.Close(websocket.StatusNormalClosure, "")

		logger.Debug().Msg("websocket message processing about to start...")
		subscriptionError := ws.processMessages(g.Request.Context(), connId, &wsIOAdapter{wsConn}, onMessageReceived) // we block here until Error or Done
		logger.Debug().Stack().Err(subscriptionError).Msg("failed to process websocket message")

		notifyAppOfWsConnectionChange(appUrls.disconnected(), connId, g, logger)
	}
}

func pushHandler(authenticateBackend func(c *gin.Context) error, connIdPathParamName string, ws *wsConnections) gin.HandlerFunc {
	return func(g *gin.Context) {

		logger := zerolog.Ctx(g.Request.Context()).With().Str("server pushing", g.Request.RemoteAddr).Logger()

		connectionIdStr := g.Param(connIdPathParamName)
		if connectionIdStr == "" {
			logger.Info().Str("param_name", connIdPathParamName).Msg("missing path param")
			g.AbortWithStatus(http.StatusBadRequest)
			return
		}

		requestBody, errReadRequest := io.ReadAll(g.Request.Body)
		if errReadRequest != nil {
			logger.Error().Str("body_type", fmt.Sprintf("%T", g.Request.Body)).Err(errReadRequest).Msg("failed to read request body")
			g.JSON(500, nil)
			return
		}
		var body interface{}
		errBodyUnmarshal := json.Unmarshal(requestBody, &body)
		if errBodyUnmarshal != nil {
			logger.Error().Str("body_content_type", fmt.Sprintf("%T", requestBody)).Err(errBodyUnmarshal).Msg("failed to unmarshal request body")
			g.JSON(400, nil)
			return
		}

		bodyAsString, conversionOk := body.(string)
		if !conversionOk {
			logger.Error().Str("body_content_type", fmt.Sprintf("%T", requestBody)).Msg("failed to convert request body to string")
			g.JSON(400, nil)
			return
		}

		errPush := ws.push(bodyAsString, connectionID(connectionIdStr))
		if errPush == errConnectionNotFound {
			logger.Error().Str("connection_id", connectionIdStr).Msg("connection doesn't exist")
			g.AbortWithStatus(http.StatusNotFound)
			return
		}

		logger.Error().Str("connection_id", connectionIdStr).Err(errPush).Msg("failed to push to connection")
		g.AbortWithStatus(http.StatusInternalServerError)
	}
}
