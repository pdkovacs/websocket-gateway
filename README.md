# Websocket gateway

Service taking care of the management (and use) of stateful websocket connections on behalf of clustered applications with with stateless backends.

## Endpoints provided by the gateway

* `GET /connect`
  
  for client devices to open a websocket connection
  
* `POST /message/${connectionId}`
  
  for application backends to send message over a websocket connection

## The service expects the application to provide endpoints

* `POST /ws/connecting`
    
    The service relays all requests incoming at its `GET /connect`
    end point as they are to this endpoint for authentication. This endpoint
    is expected to return HTTP status `200` in the case of successful authentication.

* `POST /ws/disconnected`

  * notifies of connections lost by the gateway on a best-effort basis

* `POST /ws/message-received`

  * notifies of messages received by the gateway
