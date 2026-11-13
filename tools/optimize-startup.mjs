import fs from "node:fs";
const path = new URL("../dist/game.js", import.meta.url);
let source = fs.readFileSync(path, "utf8");
const marker = "/* golden-hour-startup-v1 */";
if (source.includes(marker)) {
  console.log("Startup optimizations already applied.");
  process.exit(0);
}
const patches = [
  [
    "function OU(o){let t=FU.get(o);",
    "function OU(o){let t=FU.get(o)||globalThis.GHBoot?.swatches?.get(o);",
  ],
  ["boot:s,k9:b.k9.debug", "boot:s,swatches:FU,k9:b.k9.debug"],
  [
    'setQuality(t){t==="auto"&&(t="medium");c9[t]&&',
    'setQuality(t){t==="auto"&&(t="medium");if(t===this.quality&&this.q===c9[t]&&this.composer)return;c9[t]&&',
  ],
  [
    "function iD(o=8){_9=o;let t=",
    "function iD(o=8){_9=o;let ghCached=globalThis.GHBoot?.restoreMaterials(r=>r.canvas?new Es(globalThis.GHBoot.restoreCanvas(r)):new Qs(r.data,r.width,r.height),o);if(ghCached){Ga=yb(11,3,2);yn=yb(23,6,3);En=yb(37,16,4);qo=yb(53,48,2);Va=yb(71,5,4);[E9,T9]=ZY(5,16);return ghCached}let t=",
  ],
  [
    't.screen=Rj(),s("screen"),t}',
    't.screen=Rj(),s("screen"),globalThis.GHBoot?.captureMaterials(t),t}',
  ],
  [
    'build(t=["ar","smg","shotgun","pistol"]){let e=performance.now();',
    'build(t=["ar","smg","shotgun","pistol"]){if(globalThis.GHBoot?.hydrateArmoury(this))return 0;let e=performance.now();',
  ],
  ['this.cur=0,this.reset("ar")', "this.cur=0,this.reset(_s.primary)"],
  [
    "s.compile(t.root,this.cam,this.scene);",
    "s.compileAsync&&globalThis.GHBoot?globalThis.GHBoot.trackShader(s.compileAsync(t.root,this.cam,this.scene)):s.compile(t.root,this.cam,this.scene);",
  ],
  [
    "s.compile(a,e.camera,e.scene)",
    "s.compileAsync&&globalThis.GHBoot?globalThis.GHBoot.trackShader(s.compileAsync(a,e.camera,e.scene)):s.compile(a,e.camera,e.scene)",
  ],
  [
    "let f0=new Vs(4,4,{type:Yn});await VC(r,i,a,f0)",
    "let f0=new Vs(4,4,{type:Yn});await globalThis.GHBoot?.finishShaders();await VC(r,i,a,f0)",
  ],
];
for (const [before, after] of patches) {
  if (source.split(before).length !== 2)
    throw Error(`Expected one match: ${before}`);
  source = source.replace(before, after);
}
fs.writeFileSync(path, marker + "\n" + source);
console.log(`Applied ${patches.length} guarded startup patches.`);
