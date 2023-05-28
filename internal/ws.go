package wsgw

import (
	"context"
	"errors"
	"sync"
	"time"
	"websocket-gateway/internal/logging"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"
	"nhooyr.io/websocket"
)

type connection struct {
	id          connectionID
	fromClient  chan string
	fromBackend chan string
	readError   chan error
	closeSlow   func()
}

type wsConnections struct {
	connectionMessageBuffer int

	// publishLimiter controls the rate limit applied to the publish endpoint.
	//
	// Defaults to one publish every 100ms with a burst of 8.
	publishLimiter *rate.Limiter

	connectionsMu sync.Mutex
	wsMap         map[connectionID]*connection

	logger zerolog.Logger
}

var errConnectionNotFound = errors.New("connection not found")

func newWsConnections() *wsConnections {
	ns := &wsConnections{
		connectionMessageBuffer: 16,
		wsMap:                   make(map[connectionID]*connection),
		publishLimiter:          rate.NewLimiter(rate.Every(time.Millisecond*100), 8),
		logger:                  logging.Get().With().Str("unit", "WsConnections").Logger(),
	}

	return ns
}

type wsIO interface {
	Close() error
	Write(ctx context.Context, msg string) error
	Read(ctx context.Context) (string, error)
}

type onMgsReceivedFunc func(msg string, connectionId connectionID) error

func (wsconn *wsConnections) processMessages(
	ctx context.Context,
	connectionId connectionID,
	wsIo wsIO,
	onMessageReceived onMgsReceivedFunc,
) error {
	logger := wsconn.logger.With().Str(logging.MethodLogger, "processMessages").Logger()

	conn := &connection{
		id:          connectionId,
		fromClient:  make(chan string),
		fromBackend: make(chan string, wsconn.connectionMessageBuffer),
		readError:   make(chan error),
		closeSlow: func() {
			wsIo.Close()
		},
	}

	wsconn.addConnection(conn)
	defer wsconn.deleteConnection(conn)

	go func() {
		for {
			msgRead, errRead := wsIo.Read(ctx)
			if errRead != nil {
				switch websocket.CloseStatus(errRead) {
				case websocket.StatusNormalClosure:
					logger.Info().Str("reason", "StatusNormalClosure").Msg("client closed connection")
				case websocket.StatusGoingAway:
					logger.Info().Str("reason", "StatusGoingAway").Msg("client closed connection")
				default:
					logger.Error().Err(errRead).Msg("read error")
				}
				select {
				case conn.readError <- errRead:
				default:
					go wsIo.Close()
				}
				return
			}
			conn.fromClient <- msgRead
		}
	}()

	for {
		logger.Debug().Msg("about to enter select...")
		select {
		case msg := <-conn.fromBackend:
			logger.Debug().Msg("select: msg from backend")
			err := writeTimeout(ctx, time.Second*5, wsIo, msg)
			if err != nil {
				return err
			}
		case msg := <-conn.fromClient:
			onMessageReceived(msg, conn.id)
		case <-conn.readError:
			return nil
		case <-ctx.Done():
			logger.Debug().Msg("select: context is done")
			return ctx.Err()
		}
		logger.Debug().Msg("exited select")
	}
}

// addConnection registers a subscriber.
func (wsconn *wsConnections) addConnection(conn *connection) {
	wsconn.connectionsMu.Lock()
	wsconn.wsMap[conn.id] = conn
	wsconn.connectionsMu.Unlock()
}

// deleteConnection deletes the given subscriber.
func (wsconn *wsConnections) deleteConnection(conn *connection) {
	wsconn.connectionsMu.Lock()
	delete(wsconn.wsMap, conn.id)
	wsconn.connectionsMu.Unlock()
}

// publish publishes the msg to all subscribers.
// It never blocks and so messages to slow subscribers
// are dropped.
func (wsconn *wsConnections) push(msg string, connId connectionID) error {
	wsconn.connectionsMu.Lock()
	defer wsconn.connectionsMu.Unlock()

	wsconn.publishLimiter.Wait(context.Background())

	for id := range wsconn.wsMap {
		if id == connId {
			conn := wsconn.wsMap[id]
			conn.fromBackend <- string(msg)
			return nil
		}
	}

	return errConnectionNotFound
}

func writeTimeout(ctx context.Context, timeout time.Duration, sIo wsIO, msg string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return sIo.Write(ctx, msg)
}
