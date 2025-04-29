## MessagePack RPC Connection `msgpackrpc`

This package implements a MessagePack RPC point-to-point communication. The actors on each side of the communication can perform RPC calls to the other side so, technically, they can act simultaneously as client or server.  
By the way, for the sake of simplicity, from now on we will refer to them simply with "client").

The protocol supported is defined here https://github.com/msgpack-rpc/msgpack-rpc/blob/master/spec.md. We have 3 types of messages defined as:
* REQUEST: this message is an array of 4 elements containing in order:
  1. `type`: Fixed as number `0` (to identify the message as a REQUEST).
  2. `msgid`: A message ID, a 32-bit unsigned integer used as a sequence number to match the response (the server's response to the REQUEST will have the same `msgid`).
  3. `method`: A string containing the called method name.
  4. `params`: An array of the function arguments.

* RESPONSE: this message is an array of 4 elements containing in order:
  1. `type`: Fixed as number `1`  (to identify the message as a RESPONSE).
  2. `msgid`: A message ID.
  3. `error`: The error returned from the method, or `nil` if the method was successful.
  4. `result`: The result of the method. It should be `nil` if an error occurred.

* NOTIFICATION: this message is an array of 3 elements containing in order:
  1. `type`: Fixed number `2` (to identify this message as a NOTIFICATION).
  2. `methods`: The method name.
  3. `params`: An array of the function parameters.

### RPC Request cancelation support

The MessagePack RPC protocol implemented in this package provides also a way for a client to cancel a REQUEST. To do so the client must send a NOTIFICATION to the `$/cancel` method with a single parameter matching the `msgid` of the REQUEST to cancel.

```
[2 "$/cancel" [ MSGID ]]
```

The server will send an interrupt to the subroutine handling the original REQUEST to inform that the client is no longer interested in the RESPONSE. The server could return immediately an empty RESPONSE with an "interrupted" error, or it may ignore the cancel notification, in this latter case the cancelation will not produce any visible effect.

## MessagePack RPC Router `msgpackrouter`

This package implements a MessagePack RPC Router. A Router allows RPC calls between multiple MessagePack RPC clients, connected together in a star topology network, where the Router is the central node.

Each client can connect to the Router and expose RPC services by registering his methods using a special RPC call implemented in the Router. During normal operation, when the Router receives an RPC request, it redirects the request to the client that has previously registered the corresponding method and it will forwards back the response to the client that originated the RPC request.

### Methods implemented in the Router

The Router implements a single `$/register` method that is used by a client to register the RPC calls it wants to expose. A single string parameter is required in the call: the method name to register.

| Client P <-> Router                                                 |
| ------------------------------------------------------------------- |
| `[REQUEST, 50, "$/register", ["ping"]]` >>                          |
| Method successfully registered:<br> `[RESPONSE, 50, null, null]` << |
| Error:<br> `[RESPONSE, 50, null, "route already exists: ping"]` <<  |

After the method is registered another client may perform an RPC request to that method, the Router will take care to forward the messages back and forth. A typical RPC call example may be:

| Client A <-> Router                                                              | Router <-> Client P                                                              |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Client A does an RPC call to the Router<br>`[REQUEST, 51, "ping", [1, true]]` >> |                                                                                  |
|                                                                                  | Router forwards the request to Client P<br>>> `[REQUEST, 32, "ping", [1, true]]` |
|                                                                                  | Client P process the request and replies<br><< `[RESPONSE, 32, null, [1, true]]` |
| The Router forwards back the response<br> `[RESPONSE, 51, null, [1, true]]` <<   |                                                                                  |

Note that the request ID has been remapped by the Router: it keeps track of all active requests so the message IDs will not conflict between different clients.

## Unregistering methods

When a client disconnects all the registered methods from that client are dropped.
A request to a non-registered method will result in an error:

| Client A <-> Router                                                                                        |
| ---------------------------------------------------------------------------------------------------------- |
| Client A does an RPC call to the Router<br>`[REQUEST, 51, "xxxx", [1, true]]` >>                           |
| The Router didn't know how to handle the request<br> `[RESPONSE, 51, "method xxxx not available", nil]` << |
