#!/usr/bin/env python3
"""CNI IPv6-only acceptance on a fresh ACK BYO dual-stack test cluster.

Requires kubectl, Python 3 locally, and a test image containing Python 3.
Restarts Terway components and creates/deletes only its own test namespace.
Run separately for default and crd IPAM; never changes the cluster IP stack.
"""

import argparse
import ipaddress
import json
import pathlib
import subprocess
import sys
import time
import uuid


SERVER = """
import http.server, socket, socketserver, threading
class Server(http.server.ThreadingHTTPServer):
    address_family = socket.AF_INET6
    def server_bind(self):
        # Readiness must not depend on reverse DNS before the DNS check runs.
        socketserver.TCPServer.server_bind(self)
        self.server_name = socket.gethostname()
        self.server_port = self.server_address[1]
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(socket.gethostname().encode())
def udp():
    s = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
    s.bind(('::', 8081))
    while True:
        data, peer = s.recvfrom(4096)
        s.sendto(data, peer)
threading.Thread(target=udp, daemon=True).start()
Server(('::', 8080), Handler).serve_forever()
"""

CHECK_NETWORK = """
import errno, fcntl, ipaddress, pathlib, socket, struct
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for _, name in socket.if_nameindex():
    if name == 'lo':
        continue
    try:
        address = fcntl.ioctl(s, 0x8915, struct.pack('256s', name.encode()))
    except OSError as e:
        assert e.errno == errno.EADDRNOTAVAIL, (name, e)
    else:
        raise AssertionError((name, socket.inet_ntoa(address[20:24])))
assert len(pathlib.Path('/proc/net/route').read_text().splitlines()) == 1, 'unexpected IPv4 route'
assert any(line.split()[-1] == 'eth0' and line.split()[3] == '00'
           for line in pathlib.Path('/proc/net/if_inet6').read_text().splitlines()), 'no global IPv6 on eth0'
print('IPv6-only interface and routes verified')
"""

CHECK_PEER = """
import http.client, socket, sys
peer = sys.argv[1]
c = http.client.HTTPConnection(peer, 8080, timeout=10)
c.request('GET', '/')
r = c.getresponse()
assert r.status == 200
print(r.read().decode())
c.close()
s = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
s.settimeout(10)
s.sendto(b'ipv6-only', (peer, 8081))
assert s.recv(4096) == b'ipv6-only'
print('TCP and UDP verified')
"""


def kubectl(*args, obj=None):
    result = subprocess.run(
        ["kubectl", "--request-timeout=30s", *args],
        input=None if obj is None else json.dumps(obj),
        text=True, capture_output=True, timeout=660,
    )
    if result.returncode:
        raise RuntimeError(f"kubectl {' '.join(args)}: {result.stderr}")
    return result.stdout


def get(*args):
    return json.loads(kubectl("get", *args, "-o", "json"))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ipam", choices=["default", "crd"], required=True)
    parser.add_argument("--image", required=True, help="test image containing python3")
    parser.add_argument("--pods-per-node", type=int, required=True,
                        help="exceed the instance IPv6-per-ENI quota to test ENI expansion")
    parser.add_argument("--artifacts", required=True)
    parser.add_argument("--check-cluster-services", action="store_true",
                        help="also require IPv6 DNS and Kubernetes API frontends")
    args = parser.parse_args()
    if args.pods_per_node < 2:
        parser.error("--pods-per-node must be at least 2")
    artifacts = pathlib.Path(args.artifacts)
    artifacts.mkdir(parents=True, exist_ok=True)
    config = json.loads(get("cm", "eni-config", "-n", "kube-system")["data"]["eni_conf"])
    assert config["ip_stack"] == "ipv6", "Terway must be installed as IPv6-only"
    assert (config.get("ipam_type") or "default") == args.ipam, "IPAM mismatch"
    nodes = [n for n in get("nodes")["items"]
             if n["metadata"].get("labels", {}).get("kubernetes.io/os") == "linux"
             and n["metadata"].get("labels", {}).get("k8s.aliyun.com/exclusive-mode-eni-type") != "eniOnly"
             and not n["metadata"].get("labels", {}).get("terway-config")
             and not n["spec"].get("unschedulable")
             and not any(t["effect"] in ("NoSchedule", "NoExecute") for t in n["spec"].get("taints", []))
             and any(c["type"] == "Ready" and c["status"] == "True" for c in n["status"]["conditions"])]
    assert len(nodes) >= 2, "two ready, schedulable Linux workers required"
    namespace = "terway-ipv6-" + uuid.uuid4().hex[:8]

    def create(obj):
        obj.setdefault("metadata", {})["namespace"] = namespace
        return kubectl("create", "-f", "-", obj=obj)

    def pod(name, node):
        create({"apiVersion": "v1", "kind": "Pod", "metadata": {
            "name": name, "labels": {"app": "ipv6-only-test"}}, "spec": {
            "nodeName": node, "terminationGracePeriodSeconds": 1,
            "containers": [{"name": "test", "image": args.image,
                            "command": ["python3", "-u", "-c", SERVER],
                            "readinessProbe": {"tcpSocket": {"port": 8080}, "periodSeconds": 2}}]}})

    def execute(name, code, *argv):
        return kubectl("exec", "-n", namespace, name, "--", "python3", "-c", code, *argv)

    def wait_pods():
        kubectl("wait", "-n", namespace, "pod", "--all", "--for=condition=Ready", "--timeout=600s")
        pods = get("pods", "-n", namespace)["items"]
        ips = {}
        for p in pods:
            addresses = p["status"]["podIPs"]
            assert len(addresses) == 1 and ipaddress.ip_address(addresses[0]["ip"]).version == 6, p["metadata"]["name"]
            assert p["status"]["podIP"] == addresses[0]["ip"]
            ips[p["metadata"]["name"]] = addresses[0]["ip"]
            execute(p["metadata"]["name"], CHECK_NETWORK)
        assert len(set(ips.values())) == len(ips), "duplicate IPv6 allocation"
        return ips

    def connectivity(ips):
        for source, target in [("n0-p0", "n0-p1"), ("n0-p1", "n0-p0"),
                               ("n0-p0", "n1-p0"), ("n1-p0", "n0-p0")]:
            execute(source, CHECK_PEER, ips[target])
        service = get("svc", "ipv6-only", "-n", namespace)
        assert service["spec"]["ipFamilies"] == ["IPv6"]
        execute("n0-p0", CHECK_PEER, service["spec"]["clusterIP"])
        slices = get("endpointslices", "-n", namespace, "-l", "kubernetes.io/service-name=ipv6-only")["items"]
        assert slices and all(s["addressType"] == "IPv6" for s in slices)
        if args.check_cluster_services:
            execute("n0-p0", """
import ipaddress, pathlib, socket
servers = [l.split()[1] for l in pathlib.Path('/etc/resolv.conf').read_text().splitlines() if l.startswith('nameserver ')]
assert servers and all(ipaddress.ip_address(s).version == 6 for s in servers), ('DNS requires IPv6 nameservers', servers)
assert socket.getaddrinfo('ipv6-only', 8080, socket.AF_INET6)
""")
            execute("n0-p0", CHECK_PEER, "ipv6-only")

    def verify_connectivity(ips):
        deadline = time.monotonic() + 90
        while True:
            try:
                connectivity(ips)
                return
            except (RuntimeError, AssertionError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(3)

    kubectl("create", "namespace", namespace)
    try:
        print(f"Testing {args.ipam} in {namespace}", flush=True)
        create({"apiVersion": "v1", "kind": "Service", "metadata": {"name": "ipv6-only"},
                "spec": {"ipFamilyPolicy": "SingleStack", "ipFamilies": ["IPv6"],
                         "selector": {"app": "ipv6-only-test"}, "ports": [
                             {"name": "tcp", "port": 8080},
                             {"name": "udp", "port": 8081, "protocol": "UDP"}]}})
        for i, node in enumerate(nodes[:2]):
            for j in range(2):
                pod(f"n{i}-p{j}", node["metadata"]["name"])
        verify_connectivity(wait_pods())
        for i, node in enumerate(nodes[:2]):
            for j in range(2, args.pods_per_node):
                pod(f"n{i}-p{j}", node["metadata"]["name"])
        before = wait_pods()
        (artifacts / "expanded-pods.json").write_text(json.dumps(get("pods", "-n", namespace), indent=2))
        print(f"Expanded to {len(before)} IPv6-only Pods", flush=True)
        verify_connectivity(before)
        for resource in (["deployment/terway-controlplane"] if args.ipam == "crd" else []) + ["daemonset/terway-eniip"]:
            kubectl("rollout", "restart", resource, "-n", "kube-system")
            kubectl("rollout", "status", resource, "-n", "kube-system", "--timeout=600s")
            assert wait_pods() == before, "running Pod addresses changed after restart"
            verify_connectivity(before)
            print(f"Recovered after restarting {resource}", flush=True)
        # Delete and recreate after recovery, verifying subsequent allocation.
        kubectl("delete", "pod", "n0-p1", "-n", namespace, "--wait=true")
        pod("n0-p1", nodes[0]["metadata"]["name"])
        verify_connectivity(wait_pods())
        # Scale back down without changing pool configuration.
        for i in range(2):
            for j in range(2, args.pods_per_node):
                kubectl("delete", "pod", f"n{i}-p{j}", "-n", namespace, "--wait=true")
        verify_connectivity(wait_pods())
        if args.check_cluster_services:
            # IPv6-only Pods need an IPv6 API endpoint even though ACK remains dual-stack.
            api = get("service", "kubernetes", "-n", "default")
            api6 = [ip for ip in api["spec"].get("clusterIPs", []) if ipaddress.ip_address(ip).version == 6]
            assert api6, "environment prerequisite: kubernetes Service needs a reachable IPv6 endpoint"
            execute("n0-p0", """
import http.client, pathlib, ssl, sys
root = pathlib.Path('/var/run/secrets/kubernetes.io/serviceaccount')
c = http.client.HTTPSConnection(sys.argv[1], 443, context=ssl.create_default_context(cafile=str(root / 'ca.crt')), timeout=10)
c.request('GET', '/version', headers={'Authorization': 'Bearer ' + (root / 'token').read_text()})
r = c.getresponse()
assert r.status == 200, r.status
print(r.read().decode())
""", api6[0])
        print("PASS: IPv6-only addressing, TCP/UDP, Service, scale and restart recovery", flush=True)
    finally:
        for filename, query in [("pods.json", ("pods", "-n", namespace)),
                                ("events.json", ("events", "-n", namespace)),
                                ("nodes.json", ("nodes",)),
                                ("eni-state.json", ("nodes.network.alibabacloud.com",))]:
            try:
                (artifacts / filename).write_text(json.dumps(get(*query), indent=2))
            except Exception as error:
                print(f"Could not collect {filename}: {error}", file=sys.stderr)
        kubectl("delete", "namespace", namespace, "--wait=true", "--timeout=300s")


if __name__ == "__main__":
    main()
