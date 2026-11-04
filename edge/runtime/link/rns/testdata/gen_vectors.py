# Generates vectors_0.9.4.json with Reticulum 0.9.4 (the last MIT release):
#   uv run --no-project --with rns==0.9.4 python gen_vectors.py > vectors_0.9.4.json
import hashlib, json, os, tempfile
import RNS

configdir = tempfile.mkdtemp()
with open(os.path.join(configdir, "config"), "w") as f:
    f.write("[reticulum]\n  enable_transport = No\n  share_instance = No\n[logging]\n  loglevel = 0\n[interfaces]\n")
RNS.Reticulum(configdir=configdir, loglevel=0)

private = hashlib.sha512(b"zequent rns test identity").digest()
identity = RNS.Identity.from_bytes(private)
message = b"zequent signs this"
plaintext = b"hello from python 0.9.4"
destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "zequent", "edge", "link")
announce = destination.announce(app_data=b"app-data", send=False)
announce.pack()
data = RNS.Packet(destination, b"x", create_receipt=False)
data.pack()

print(json.dumps({
    "rns_version": RNS.__version__,
    "private_key": private.hex(),
    "public_key": identity.get_public_key().hex(),
    "identity_hash": identity.hash.hex(),
    "name_hash": destination.name_hash.hex(),
    "destination_hash": destination.hash.hex(),
    "plain_destination_hash": RNS.Destination.hash(None, "zequent", "edge", "link").hex(),
    "message": message.hex(),
    "signature": identity.sign(message).hex(),
    "plaintext": plaintext.hex(),
    "ciphertext": identity.encrypt(plaintext).hex(),
    "announce_raw": announce.raw.hex(),
    "announce_hash": announce.get_hash().hex(),
    "data_raw": data.raw.hex(),
    "data_hash": data.get_hash().hex(),
}, indent=2))
