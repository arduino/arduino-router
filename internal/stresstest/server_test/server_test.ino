/*
    Test sketch for the arduino-router.

    - Sends "payload" notifications, carrying 250 bytes of binary data, at a
      rate that can be changed with the "set_payload_rate" method (initially
      DEFAULT_PAYLOAD_RATE notifications per second). The payload is filled
      with a pattern derived from a sequence number: byte i = (seq + i) & 0xFF.
    - Provides the following methods:
      - "echo": answers with the same binary argument received and counts the
        received calls.
      - "null": receives notifications with a binary argument, and just counts
        them.
      - "set_payload_rate": sets the rate of the "payload" notifications, in
        notifications per second (0 = stopped, rates above 1000000 mean "as
        fast as possible").
      - "stats": returns a map with the counters:
        {"echo": <echo calls>, "null": <null notifications>, "sent": <payload
        notifications sent>, "rate": <current payload rate>, "calls_ok":
        <successful calls to the router>, "calls_failed": <failed calls to the
        router>}
      - "link_stats": returns a map with the state of the seriallink protocol:
        {"session", "baud_rate", "tx_frames", "rx_frames", "rx_errors",
        "retransmits", "baud_switches", "baud_failures", "baud_fallbacks"}. The
        map is empty with BridgeProtocol::Raw.
    - With BridgeProtocol::Reliable, calls the router ($/version) every
      ROUTER_CALL_INTERVAL milliseconds, to test the calls in the MCU -> router
      direction, counting successes and failures. They are not done with
      BridgeProtocol::Raw: a call has no timeout, so a lost response would block
      the sketch forever.

    The router must run with --serial-protocol=auto (or the protocol used by the
    sketch), and with --serial-max-baudrate to negotiate a faster rate.

    Note: the requests received by the MCU are limited to BRIDGE_RPC_BUFFER_SIZE
    (256 bytes) including the msgpack-rpc envelope, larger requests are dropped.
*/

#include <Arduino_RouterBridge.h>
#include <zephyr/sys/atomic.h>

#define DEFAULT_PAYLOAD_RATE 10
#define PAYLOAD_SIZE        250
#define ROUTER_CALL_INTERVAL 100

// The protocol used to talk with the router (BridgeProtocol::Raw or
// BridgeProtocol::Reliable).
#define PROTOCOL            BridgeProtocol::Reliable

// The baud rate: the maximum rate negotiated with the router with
// BridgeProtocol::Reliable, the rate of the port with BridgeProtocol::Raw
// (0 for the library default).
#ifndef MAX_BAUD
#define MAX_BAUD            0
#endif

typedef MsgPack::bin_t<uint8_t> bytes_t;

atomic_t echo_calls = ATOMIC_INIT(0);
atomic_t null_notifications = ATOMIC_INIT(0);
atomic_t sent_notifications = ATOMIC_INIT(0);
atomic_t payload_rate = ATOMIC_INIT(DEFAULT_PAYLOAD_RATE);
atomic_t calls_ok = ATOMIC_INIT(0);
atomic_t calls_failed = ATOMIC_INIT(0);

String echo(String data) {
    atomic_inc(&echo_calls);
    return data;
}

void null(bytes_t data) {
    (void)data;
    atomic_inc(&null_notifications);
}

void set_payload_rate(uint32_t rate) {
    atomic_set(&payload_rate, rate);
}

MsgPack::map_t<MsgPack::str_t, uint32_t> stats() {
    MsgPack::map_t<MsgPack::str_t, uint32_t> out;
    out["echo"] = atomic_get(&echo_calls);
    out["null"] = atomic_get(&null_notifications);
    out["sent"] = atomic_get(&sent_notifications);
    out["rate"] = atomic_get(&payload_rate);
    out["calls_ok"] = atomic_get(&calls_ok);
    out["calls_failed"] = atomic_get(&calls_failed);
    return out;
}

MsgPack::map_t<MsgPack::str_t, uint32_t> link_stats() {
    MsgPack::map_t<MsgPack::str_t, uint32_t> out;
    SerialLinkTransport* link = Bridge.getLinkTransport();
    if (link == nullptr) {
        return out;
    }
    const seriallink::Stats s = link->stats();
    out["session"] = link->session();
    out["baud_rate"] = link->baudRate();
    out["tx_frames"] = s.txFrames;
    out["rx_frames"] = s.rxFrames;
    out["rx_errors"] = s.rxErrors;
    out["retransmits"] = s.retransmits;
    out["baud_switches"] = s.baudSwitches;
    out["baud_failures"] = s.baudFailures;
    out["baud_fallbacks"] = s.baudFallbacks;
    return out;
}

void setup() {
    pinMode(LED_BUILTIN, OUTPUT);

    if (!Bridge.begin(MAX_BAUD, PROTOCOL)) {
        // Blink fast forever: the router did not answer
        while (true) {
            digitalWrite(LED_BUILTIN, !digitalRead(LED_BUILTIN));
            delay(100);
        }
    }

    Bridge.provide("echo", echo);
    Bridge.provide("null", null);
    Bridge.provide("set_payload_rate", set_payload_rate);
    Bridge.provide("stats", stats);
    Bridge.provide("link_stats", link_stats);
}

void call_router() {
    static unsigned long last = 0;

    if (Bridge.getLinkTransport() == nullptr || millis() - last < ROUTER_CALL_INTERVAL) {
        return;
    }
    last = millis();

    MsgPack::str_t version;
    if (Bridge.getRouterVersion(version)) {
        atomic_inc(&calls_ok);
    } else {
        atomic_inc(&calls_failed);
    }
}

void send_payload() {
    static uint32_t seq = 0;
    static unsigned long last = 0;

    const uint32_t rate = (uint32_t)atomic_get(&payload_rate);
    if (rate == 0) {
        last = 0;
        delay(1);
        return;
    }

    const unsigned long interval_us = 1000000UL / rate;
    const unsigned long now = micros();
    if (last == 0) {
        last = now;
    }
    if (now - last < interval_us) {
        if (interval_us - (now - last) > 5000) {
            delay(5);
            return;
        }
        delayMicroseconds(interval_us - (now - last));
    }
    last += interval_us;

    bytes_t payload(PAYLOAD_SIZE);
    for (size_t i = 0; i < PAYLOAD_SIZE; i++) {
        payload[i] = (uint8_t)(seq + i);
    }
    Bridge.notify("payload", payload);
    atomic_inc(&sent_notifications);
    seq++;

    digitalWrite(LED_BUILTIN, seq & 0x08 ? HIGH : LOW);
}

void loop() {
    call_router();
    send_payload();
}
