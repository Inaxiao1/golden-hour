// One-time authoring tool: open the printed local URL and wait for completion.
// All generated files come from the shipped renderer, no external assets.
import http from "node:http";
import fs from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";
const root = path.resolve(import.meta.dirname, "..");
const exportCode = `
const wait=setInterval(async()=>{
 if(!window.GH?.ready)return;clearInterval(wait);
 const studio=GH.debug.ctx.hud.studio;
 const data={schema:1,flat:Array.from(studio.flat),cards:Array.from(studio.cards),shaded:studio.shaded,swatches:Array.from(GH.debug.swatches,([key,canvas])=>[key,canvas.toDataURL('image/png')])};
 const response=await fetch('/__bake',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
 const result=document.createElement('p');result.id='bake-result';result.textContent=response.ok?'启动资源预烘焙完成，可关闭此页。':'预烘焙失败';result.style='position:fixed;inset:32px;z-index:9999;background:#14251e;padding:24px;color:white';document.body.append(result);
},100);`;
http
  .createServer(async (req, res) => {
    try {
      if (req.url === "/__bake" && req.method === "POST") {
        let data = "";
        for await (const chunk of req) {
          data += chunk;
          if (data.length > 8e6) throw Error("too large");
        }
        const value = JSON.parse(data);
        if (value.schema !== 1 || value.flat.length < 5 || !value.shaded.frag)
          throw Error("invalid pack");
        value.source = createHash("sha256")
          .update(await fs.readFile(path.join(root, "dist/game.js")))
          .digest("hex");
        await fs.mkdir(path.join(root, "assets"), { recursive: true });
        await fs.writeFile(
          path.join(root, "assets/armoury.json"),
          JSON.stringify(value),
        );
        console.log(
          "Baked assets/armoury.json:",
          Buffer.byteLength(data),
          "bytes",
        );
        res.end("ok");
        return;
      }
      if (req.url === "/__bake.js") {
        res.setHeader("Content-Type", "text/javascript");
        res.end(exportCode);
        return;
      }
      const pathname = decodeURIComponent(
        new URL(req.url, "http://localhost").pathname,
      );
      const name = pathname === "/" ? "index.html" : pathname.slice(1);
      if (
        !["index.html", "i18n-zh.js", "dist/game.js"].includes(name) &&
        !/^client\/[a-z-]+\.(js|css)$/.test(name)
      ) {
        res.writeHead(404).end();
        return;
      }
      let data = await fs.readFile(path.join(root, name));
      res.setHeader(
        "Content-Type",
        name.endsWith(".html")
          ? "text/html"
          : name.endsWith(".css")
            ? "text/css"
            : "text/javascript",
      );
      if (name === "index.html")
        data = Buffer.from(
          data
            .toString()
            .replace("</body>", '<script src="/__bake.js"></script></body>'),
        );
      res.end(data);
    } catch (error) {
      res.writeHead(500).end(String(error));
    }
  })
  .listen(3002, "127.0.0.1", () =>
    console.log("Bake startup assets: http://localhost:3002/?bake=1"),
  );
