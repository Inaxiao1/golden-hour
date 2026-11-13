// Rebuild the authoritative collision fixture from the shipped hotel renderer.
// Run `node tools/export-map.mjs`, open localhost:3001, and wait for the report.
import http from "node:http";
import fs from "node:fs/promises";
import path from "node:path";
const root = path.resolve(import.meta.dirname, "..");
const target = path.join(root, "internal/game/maps/hotel.json");
const script = `
const timer = setInterval(async () => {
  if (!window.GH?.ready) return;
  clearInterval(timer);
  const cols = GH.debug.world.cols.filter(c => !c.off && !c.pane).map(c => ({
    min:[c.x0,c.y0,c.z0], max:[c.x1,c.y1,c.z1], noMove:!!c.noMove, noRay:!!c.noRay
  }));
  const nodes = GH.debug.nav.nodes.map(n => ({pos:[n.x,n.y,n.z],links:n.links.map(l=>l.to)}));
  const result = await fetch('/__map', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({version:1,name:'hotel',colliders:cols,nodes})});
  const report = document.createElement('pre'); report.id='export-report';
  report.style='position:fixed;inset:20px;z-index:9999;background:#14251e;padding:24px;color:white;white-space:pre-wrap';
  report.textContent = result.ok ? '已导出酒店碰撞地图：'+cols.length+' 个碰撞体。可以关闭此页。' : '导出失败';
  document.body.append(report);
},100);`;
http
  .createServer(async (req, res) => {
    try {
      if (req.url === "/__map" && req.method === "POST") {
        let data = "";
        for await (const c of req) {
          data += c;
          if (data.length > 8e6) throw Error("too large");
        }
        const map = JSON.parse(data);
        if (!map.colliders?.length) throw Error("empty map");
        await fs.mkdir(path.dirname(target), { recursive: true });
        await fs.writeFile(target, JSON.stringify(map));
        console.log(`Exported ${map.colliders.length} colliders to ${target}`);
        res.end("ok");
        return;
      }
      if (req.url === "/__export.js") {
        res.setHeader("Content-Type", "text/javascript");
        res.end(script);
        return;
      }
      const pathname = decodeURIComponent(
        new URL(req.url, "http://localhost").pathname,
      );
      const file = path.resolve(
        root,
        "." + (pathname === "/" ? "/index.html" : pathname),
      );
      if (!file.startsWith(root + path.sep)) {
        res.writeHead(403).end();
        return;
      }
      let content = await fs.readFile(file);
      res.setHeader(
        "Content-Type",
        file.endsWith(".html")
          ? "text/html"
          : file.endsWith(".js")
            ? "text/javascript"
            : "application/octet-stream",
      );
      if (file.endsWith("index.html"))
        content = Buffer.from(
          content
            .toString()
            .replace("</body>", '<script src="/__export.js"></script></body>'),
        );
      res.end(content);
    } catch (e) {
      res.writeHead(500).end(String(e));
    }
  })
  .listen(3001, "127.0.0.1", () =>
    console.log("Map exporter: http://localhost:3001"),
  );
