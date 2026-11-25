# Interop peer for the Go rns driver (run with any rns version; used as a black box).
# Connects to a hub as a TCP client, announces "zqnttest.peer", and echoes every packet it
# receives back to the sender's "zequent.edge.link" destination learned from its announce.
# Ingress control is off because the test announces every two seconds: the hub rebroadcasts two
# announces within milliseconds, which a new interface would otherwise hold as a burst for minutes.
import sys, time
import RNS

configdir, port = sys.argv[1], sys.argv[2]
with open(configdir + "/config", "w") as f:
    f.write(f"""[reticulum]
  enable_transport = No
  share_instance = No
[logging]
  loglevel = 2
[interfaces]
  [[hub]]
    type = TCPClientInterface
    interface_enabled = Yes
    ingress_control = No
    target_host = 127.0.0.1
    target_port = {port}
""")
RNS.Reticulum(configdir=configdir)
peer = RNS.Destination(RNS.Identity(), RNS.Destination.IN, RNS.Destination.SINGLE, "zqnttest", "peer")
go_nodes = {}

class GoAnnounces:
    aspect_filter = "zequent.edge.link"
    def received_announce(self, destination_hash, announced_identity, app_data):
        go_nodes["last"] = destination_hash
        print("PEER_SAW_GO_ANNOUNCE " + destination_hash.hex(), flush=True)

def on_packet(data, packet):
    print("PEER_RX " + data.hex(), flush=True)
    target = go_nodes.get("last")
    if target is None:
        print("PEER_NO_GO_PATH", flush=True)
        return
    identity = RNS.Identity.recall(target)
    reply = RNS.Destination(identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "zequent", "edge", "link")
    RNS.Packet(reply, b"echo:" + data).send()

RNS.Transport.register_announce_handler(GoAnnounces())
peer.set_packet_callback(on_packet)
print("PEER " + peer.hash.hex() + " RNS " + RNS.__version__, flush=True)
while True:
    peer.announce()
    time.sleep(2)
