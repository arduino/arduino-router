# A MessagePack RPC client/server implementation.

This package implements a MessagePack RPC point-to-point communication. The actors on each side of the communication can perform RPC calls to the other side so, technically, they can act simultaneously as client or server. By the way, for the sake of simplicity, from now on we will refer to them simply with "client".

The protocol supported is defined here https://github.com/msgpack-rpc/msgpack-rpc/blob/master/spec.md. We have 3 types of messages defined as:

- REQUEST: this message is an array of 4 elements containing in order:
  1. `type`: Fixed as number `0` (to identify the message as a REQUEST).
  2. `msgid`: A message ID, a 32-bit unsigned integer used as a sequence number to match the response (the server's response to the REQUEST will have the same `msgid`).
  3. `method`: A string containing the called method name.
  4. `params`: An array of the function arguments.

- RESPONSE: this message is an array of 4 elements containing in order:
  1. `type`: Fixed as number `1` (to identify the message as a RESPONSE).
  2. `msgid`: A message ID.
  3. `error`: The error returned from the method, or `null` if the method was successful.
  4. `result`: The result of the method. It should be `null` if an error occurred.

- NOTIFICATION: this message is an array of 3 elements containing in order:
  1. `type`: Fixed number `2` (to identify this message as a NOTIFICATION).
  2. `methods`: The method name.
  3. `params`: An array of the function parameters.

### RPC Request cancelation support

The MessagePack RPC protocol implemented in this package provides also a way for a client to cancel a REQUEST. To do so the client must send a NOTIFICATION to the `$/cancel` method with a single parameter matching the `msgid` of the REQUEST to cancel.

```
[2 "$/cancel" [ MSGID ]]
```

The server will send an interrupt to the subroutine handling the original REQUEST to inform that the client is no longer interested in the RESPONSE. The server could return immediately an empty RESPONSE with an "interrupted" error, or it may ignore the cancel notification, in this latter case the cancelation will not produce any visible effect.
