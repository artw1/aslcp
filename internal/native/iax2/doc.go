// Package iax2 is an outbound-only IAX2 client (RFC 5456) that links a native
// node to an AllStarLink (app_rpt) node.
//
// It implements only what an end-user node needs: the call token exchange,
// MD5 (and, if the peer demands it, plaintext) authentication, G.711 ulaw
// voice in 20 ms frames, ACK and retransmission of full frames, PING/PONG
// and LAGRQ/LAGRP keepalives, HANGUP, and the app_rpt link text conventions
// carried in TEXT frames (the !NEWKEY1! keying handshake and "L" link
// lists). It never accepts calls: a NEW arriving on the client socket is
// answered with REJECT, and the socket is bound to an ephemeral port unless
// the caller asks for another.
//
// How app_rpt places a link, which this client mirrors so a stock ASL3 node
// accepts it:
//
//   - The dial string a node builds from DNS is "radio@<ip>:<port>/<node>",
//     so the NEW carries USERNAME "radio", CALLED NUMBER the far node and
//     CALLING NUMBER our node number (app_rpt sets the connected line number
//     to its own node name). The far node's iax.conf [radio] user sends the
//     call to the radio-secure context, which runs Rpt(<node>).
//   - Rpt() looks up our node number (local [nodes] stanza, then
//     _iax._udp.<node>.nodes.allstarlink.org SRV and the A record of its
//     target) and compares the IP with the IP the call came from. Only the
//     IP is compared, not the port, so an ephemeral source port is fine as
//     long as registration and the call leave from the same public IP.
//   - After ANSWER both sides send the text "!NEWKEY1!". In that mode a link
//     is keyed while voice frames arrive and unkeyed by a RADIO_UNKEY control
//     frame (or when voice stops); RADIO_KEY is not sent. A node sends voice
//     only while it transmits.
//   - Every linkpost_time (30 s by default), and shortly after a change, a
//     node sends "L <list>" naming the nodes behind it, each prefixed by T
//     (transceive), R (receive only) or C (connecting).
//
// Audio crosses the package boundary as signed 16-bit linear PCM, mono,
// 8000 Hz, in 20 ms frames of 160 samples (Frame).
package iax2
