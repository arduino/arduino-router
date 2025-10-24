# `arduino-router` is a MessagePack RPC Router

This module implements a MessagePack RPC Router that allows RPC calls between multiple MessagePack RPC clients, connected together in a star topology network, where the Router is the central node.

Each client can connect to the Router and expose RPC services by registering his methods using a special RPC call implemented in the Router. During normal operation, when the Router receives an RPC request, it redirects the request to the client that has previously registered the corresponding method and it will forwards back the response to the client that originated the RPC request.

To understand more about MessagePack encoding see: <https://msgpack.org/>

This package provides also a MessagePack RPC client in `msgpackrpc` package. To get more details about MessagePack RPC and this implementation see [here](msgpackrpc/README.md).

### Methods implemented in the Router

The Router implements a single `$/register` method that is used by a client to register the RPC calls it wants to expose. A single string parameter is required in the call: the method name to register.

| Client P <-> Router                                                 |
| ------------------------------------------------------------------- |
| `[REQUEST, 50, "$/register", ["ping"]]` >>                          |
| Method successfully registered:<br> `[RESPONSE, 50, null, true]` << |
| Error:<br> `[RESPONSE, 50, "route already exists: ping", null]` <<  |

After the method is registered another client may perform an RPC request to that method, the Router will take care to forward the messages back and forth. A typical RPC call example may be:

| Client A <-> Router                                                              | Router <-> Client P                                                              |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Client A does an RPC call to the Router<br>`[REQUEST, 32, "ping", [1, true]]` >> |                                                                                  |
|                                                                                  | Router forwards the request to Client P<br>>> `[REQUEST, 51, "ping", [1, true]]` |
|                                                                                  | Client P process the request and replies<br><< `[RESPONSE, 51, null, [1, true]]` |
| The Router forwards back the response<br> `[RESPONSE, 32, null, [1, true]]` <<   |                                                                                  |

Note that the request ID has been remapped by the Router: it keeps track of all active requests so the message IDs will not conflict between different clients.

### Calling an unregistered method

A request to a non-registered method will result in an error:

| Client A <-> Router                                                                                         |
| ----------------------------------------------------------------------------------------------------------- |
| Client A does an RPC call to the Router<br>`[REQUEST, 33, "xxxx", [1, true]]` >>                            |
| The Router didn't know how to handle the request<br> `[RESPONSE, 33, "method xxxx not available", null]` << |

### Unregistering methods (via `$/reset` method call)

A client can drop all its registered methods by calling the `$/reset` method, with an empty parameter list.

| Client A <-> Router                                                                   |
| ------------------------------------------------------------------------------------- |
| Clian A request to remove all registered methods<br>`[REQUEST, 52, "$/reset", []]` >> |
| The Router should always succeed<br> `[RESPONSE, 52, null, true]` <<                  |

### Unregistering methods (via client disconnection)

When a client disconnects all the registered methods from that client are dropped.

### Router serial connection

The MsgPack RPC Router can establish a physical connection with a serial port. This connection can register and call RPC methods as any other network TCP/IP connection. The serial port address is specified via the command line flag `-p PORT`, if this flag is set the Router will try to open the serial port at startup.

If the serial port fails for some reason, the router will retry to connect automatically after 5 seconds.

The Router has a RPC methods to "open" and "close" the serial connection on request:

- The `$/serial/open` method will open the serial port connection. This method returns immediately.
- The `$/serial/close` method will close the serial port connection. This method returns only after the port has been successfully disconnected.
