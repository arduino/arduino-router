The examples here shows how an RPC communication should work.

* `ping_server` is a MsgPack RPC server that answers to the `ping` method. It connects to the MsgPack RPC Router and registers the `"ping"` method, so other client can use it.
* `ping_client` is a MsgPack RPC client that sends a `ping` request and prints the response. It connects to the MsgPack RPC Router to send the request.
* `msgpackdump` is a MsgPack debug tool. It reads a MsgPack stream from the file path given as argument and prints the decoded stream to stdout as ASCII strings.

To test the examples above, for the current directory:

1. Open a terminal window and run the MsgPack RPC Router:
   ```
   $ go run ../main.go
   2025/04/30 16:09:10 INFO Listening for TCP/IP services listen_addr=:8900
   ```
2. Open another terminal window and run `ping_server`:
   ```
   $ go run ping_server/main.go
   2025/04/30 16:11:17 INFO Connected to router addr=:8900
   ```
3. Open another terminal window and run `ping_client`:
   ```
   $ go run ping_client/main.go
   [HELLO 1 true 5] <nil>
   ```
   The array `[HELLO 1 true 5]` is the response the the `ping` RPC request.
4. Kill the `ping_server` in the second terminal and retry `ping_client`:
   ```
   $ go run ping_client/main.go 
   <nil> method ping not available
   ```
   This time the server is not running and the registered `ping` method is no longer available.

Let's see now how `msgpackdump` can be used:

5. In this example we will show how to decode a test file called `array` that contains the array `[32 nil false "HELLO"]` encoded in MsgPack format:
   ```
   $ hexdump msgpackdump/testdata/array -C
   00000000  94 20 c0 c2 a6 48 45 4c  4c 4f 21                 |. ...HELLO!|
   0000000b
   $ go run msgpackdump/main.go msgpackdump/testdata/array
   ([]interface {}) (len=4 cap=4) {
    (int8) 32,
    (interface {}) <nil>,
    (bool) false,
    (string) (len=6) "HELLO!"
   }
   2025/04/30 16:29:11 EOF
   exit status 1
   ```