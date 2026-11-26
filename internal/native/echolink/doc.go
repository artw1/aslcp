// Package echolink is an outbound-only EchoLink client for the native node.
//
// Login keeps the node logged on to an EchoLink directory server over TCP
// port 5200 and fetches the station list used to resolve a callsign or node
// number to an address. Directory.Connect then calls a station or conference
// over UDP: GSM audio in RTP packets on port 5198 (four 20 ms frames, 80 ms,
// per packet) and RTCP on port 5199 carrying the SDES packets EchoLink peers
// use to identify each other and to keep the link up, and a BYE to end it.
// Text packets on the audio port carry station info and chat.
//
// The client never accepts calls: its UDP sockets exist only while an
// outgoing connection is up, and a station that tries to connect while we are
// talking to someone else is sent a BYE.
//
// Parts of the EchoLink wire format are not formally documented. The formats
// here follow public protocol write-ups and the behavior of open-source
// clients; comments mark the details that are inferred rather than
// specified.
package echolink
