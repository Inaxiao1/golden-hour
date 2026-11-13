// Small, guarded patches for the distributed prototype bundle. Fail on a new
// upstream build rather than silently modifying the wrong code.
import fs from "node:fs";
const path = new URL("../dist/game.js", import.meta.url);
let source = fs.readFileSync(path, "utf8");
const marker = "/* golden-hour-performance-v1 */";
if (source.includes(marker)) {
  console.log("Renderer optimizations already applied.");
  process.exit(0);
}
const patches = [
  [
    "low:{scale:.7,samples:0,shadow:2048,bloom:!0",
    "low:{scale:.7,samples:0,shadow:1024,bloom:!1",
  ],
  [
    "medium:{scale:.85,samples:2,shadow:2048,bloom:!0,sharpen:.3,maxDpr:1,ao:!1,refl:.5,rays:24,soft:!0,vol:12,distort:!0,mblur:0,dirt:.26,streak:0,dof:1,atlas:2048,fxK:.8,vmblur:6}",
    "medium:{scale:.85,samples:2,shadow:2048,bloom:!0,sharpen:.3,maxDpr:1,ao:!1,refl:.25,rays:16,soft:!0,vol:8,distort:!1,mblur:0,dirt:.26,streak:0,dof:0,atlas:2048,fxK:.7,vmblur:0}",
  ],
  [
    'this.quality="high",this.q=c9.high',
    'this.quality="medium",this.q=c9.medium',
  ],
  [
    'quality:"high",motionBlur:"high",difficulty:',
    'quality:"auto",motionBlur:"off",renderVersion:1,difficulty:',
  ],
  [
    'try{Object.assign(_s,JSON.parse(localStorage.getItem(QD)||"{}"))}catch{}',
    'try{let ghSaved=JSON.parse(localStorage.getItem(QD)||"{}");Object.assign(_s,ghSaved);if(!ghSaved.renderVersion){_s.quality="auto";_s.motionBlur="off";_s.renderVersion=1;localStorage.setItem(QD,JSON.stringify(_s))}}catch{}',
  ],
  ["setQuality(t){c9[t]&&", 'setQuality(t){t==="auto"&&(t="medium");c9[t]&&'],
  [
    'label:"QUALITY PRESET",type:"seg",opts:[["low","LOW"]',
    'label:"QUALITY PRESET",type:"seg",opts:[["auto","自动"],["low","LOW"]',
  ],
  [
    "BU={low:",
    'BU={auto:"自动根据对局帧率降低画质与分辨率，优先保持操作流畅。可随时手动选择画质。",low:',
  ],
  [
    "70% resolution, no anti-aliasing, no ambient occlusion or floor reflections. For integrated graphics.",
    "70% 分辨率、1K 阴影；关闭泛光、环境光遮蔽和地面反射，适合低配设备。",
  ],
  [
    "85% resolution, 2\\xD7 MSAA, half-rate floor reflections and light shafts.",
    "85% 分辨率、2× MSAA、低分辨率反射；关闭景深与运动模糊通道。",
  ],
  [
    "e.info.reset(),e.shadowMap.needsUpdate=!0,this.preRender&&",
    'e.info.reset(),e.shadowMap.needsUpdate=e.shadowMap.needsUpdate||!this._shadowAt||this.time-this._shadowAt>=(this.quality==="low"?.1:this.quality==="medium"?.05:1/60),e.shadowMap.needsUpdate&&(this._shadowAt=this.time),this.preRender&&(this.quality!=="medium"||!this._reflectAt||this.time-this._reflectAt>=1/30)&&(this._reflectAt=this.time,!0)&&',
  ],
  [
    "function M0(){let f0=Math.min(G.getDelta(),.05555555555555555);",
    'let ghNextFrame=0;function M0(ghNow){ghNow=ghNow??performance.now();let ghInterval=1000/(document.hidden?5:Je.state==="playing"?60:Je.state==="paused"||Je.state==="over"?20:30);if(ghNow+.5<ghNextFrame)return;ghNextFrame=Math.max(ghNextFrame+ghInterval,ghNow+ghInterval*.2);let f0=Math.min(G.getDelta(),.05555555555555555);',
  ],
];
for (const [before, after] of patches) {
  if (source.split(before).length !== 2)
    throw Error(`Expected one match: ${before}`);
  source = source.replace(before, after);
}
fs.writeFileSync(path, marker + "\n" + source);
console.log(`Applied ${patches.length} guarded renderer patches.`);
