# Repeater gateway test

Checks RefConnect's repeater links against a real repeater gateway, with no
radio or hotspot. `run.sh` builds [DStarGateway](https://github.com/F4FXL/DStarGateway)
(the maintained port of G4KLX ircDDBGateway) in Docker, serving repeater
`N0CALL` module B, and runs

    go test -tags gateway -race -run Gateway ./internal/router

which links over DPlus, DExtra and DCS, keys up through the router, and checks
that the gateway puts the transmission on the air. The repeater is the test
itself: the gateway sends what it would transmit to UDP 20011 on the host as
homebrew (DSRP) packets. A control case checks that the old reflector-style
header (RPT2 = `REF001 B`) is dropped.

    ./run.sh               # needs Docker running
    RUN=Gateway/DCS ./run.sh
    KEEP=1 ./run.sh        # leave the gateway up afterwards

With `KEEP=1` you can link the app to it by hand: Repeater mode, callsign
`N0CALL`, module `B`, host `127.0.0.1`. Stop it with
`docker rm -f refconnect-test-gateway`.

The gateway stays off the real networks. ircDDB, APRS, remote control and
host-file downloads are off, the bundled reflector lists are removed, and
`auth.dstargateway.org` (the DPlus login, which starts whenever DPlus is
enabled) resolves to 127.0.0.1.

Ports 20001, 30001 and 30051 (UDP) must be free on the host. Note that a DPlus
client on the host binds 20001 when it can, so if the gateway isn't running a
DPlus link to 127.0.0.1 reads back its own packets.
