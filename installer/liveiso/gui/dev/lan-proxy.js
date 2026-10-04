// Expose the mock dev server on a LAN address so a real phone can test the
// QR flow.  WSL2 only forwards 127.0.0.1 to Windows, so run this on Windows:
//
//   node installer/liveiso/gui/dev/lan-proxy.js 192.168.1.74:8765
//
// and start run-mock.sh with: --trust-proxy --advertise 192.168.1.74:8765
// The proxy sends each client's real address in X-Forwarded-For (replacing
// any the client sent), so phones still need the QR token.
"use strict";
const http = require("http");

const [listen, target = "127.0.0.1:8765"] = process.argv.slice(2);
if (!listen) {
  console.error("usage: node lan-proxy.js LISTEN_HOST:PORT [TARGET_HOST:PORT]");
  process.exit(2);
}
const [lhost, lport] = listen.split(":");
const [thost, tport] = target.split(":");

const server = http.createServer((req, res) => {
  const headers = { ...req.headers, "x-forwarded-for": req.socket.remoteAddress.replace(/^::ffff:/, "") };
  const up = http.request({ host: thost, port: tport, method: req.method, path: req.url, headers }, (ur) => {
    res.writeHead(ur.statusCode, ur.headers);
    ur.pipe(res);
  });
  up.on("error", (e) => {
    if (!res.headersSent) res.writeHead(502, { "Content-Type": "text/plain" });
    res.end(`Dev server not reachable at ${target}: ${e.message}\n`);
  });
  res.on("close", () => up.destroy());
  req.pipe(up);
});
server.requestTimeout = 0; // server-sent events stay open
server.listen(Number(lport), lhost, () => console.log(`http://${listen}/ -> http://${target}/`));
