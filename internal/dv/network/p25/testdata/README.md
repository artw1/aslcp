# P25 network test vectors

`vectors.txt` holds synthetic vectors generated once by a throwaway C++
harness linked against unmodified g4klx MMDVMHost sources (P25Network,
P25Data, P25LowSpeedData, P25Audio) with a stub UDP socket that recorded
every write.

- `imbeNN` (00..17): 11-byte IMBE frames, byte i of frame f =
  (f*17 + i*29 + 3) & 0xFF.
- `p25rec`: the 18 records CP25Network::writeLDU1 and writeLDU2 sent for
  those frames (LCF 0, MFId 0, source 1234567, destination 1997, AlgId
  0x80, KID 0, MI and LSD zero), then the 0x80 terminator (`end` set).

The harness and the reference clones are not part of the repository.
