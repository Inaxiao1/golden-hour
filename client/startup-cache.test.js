import { test } from "node:test";
import assert from "node:assert/strict";
import {
  captureMaterials,
  restoreMaterials,
  hydrateArmoury,
} from "./startup-cache.js";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";

const vec = (x, y) => ({
  toArray: () => [x, y],
  fromArray(v) {
    this.value = v;
  },
});
function texture(data = new Uint8Array([1, 2, 3, 4])) {
  return {
    isTexture: true,
    isDataTexture: true,
    image: { width: 1, height: 1, data },
    repeat: vec(2, 3),
    offset: vec(0.1, 0.2),
    center: vec(0, 0),
    userData: { tile: [0.5, 1] },
    flipY: false,
    colorSpace: "srgb",
    wrapS: 1000,
    generateMipmaps: true,
  };
}
const create = (record) => ({
  image: { data: record.data, width: record.width, height: record.height },
  repeat: vec(0, 0),
  offset: vec(0, 0),
  center: vec(0, 0),
});
test("material cache preserves pixels, UV settings, colour and shared textures", () => {
  const original = texture();
  const pack = captureMaterials({
    timings: { old: 99 },
    floor: { map: original, tile: [1, 2] },
    art: [original],
  });
  const result = restoreMaterials(structuredClone(pack), create, 4);
  assert.equal(pack.textures.length, 1);
  assert.equal(pack.textures[0].canvas, false);
  assert.equal(pack.bytes, 4);
  assert.deepEqual(result.floor.map.image.data, original.image.data);
  assert.equal(result.art[0], result.floor.map);
  assert.deepEqual(result.floor.map.repeat.value, [2, 3]);
  assert.equal(result.floor.map.flipY, false);
  assert.equal(result.floor.map.colorSpace, "srgb");
  assert.equal(result.floor.map.anisotropy, 4);
  assert.equal(result.floor.map.needsUpdate, true);
});
test("corrupt or obsolete material data fails safely", () => {
  assert.equal(restoreMaterials({ schema: 2 }, create, 4), null);
  const pack = captureMaterials({ map: texture() });
  const bad = structuredClone(pack);
  bad.textures[0].data = new Uint8Array(3);
  assert.throws(() => restoreMaterials(bad, create, 4), /Invalid/);
  pack.tree.map.texture = 99;
  assert.throws(() => restoreMaterials(pack, create, 4), /reference/);
});

test("canvas material pixels retain their original upload type", () => {
  const original = texture();
  original.isDataTexture = false;
  original.flipY = true;
  original.image = {
    width: 1,
    height: 1,
    getContext: () => ({
      getImageData: () => ({ data: new Uint8ClampedArray([5, 6, 7, 8]) }),
    }),
  };
  const pack = captureMaterials({ screen: original });
  assert.equal(pack.textures[0].canvas, true);
  assert.equal(pack.textures[0].properties.flipY, true);
  assert.deepEqual(Array.from(pack.textures[0].data), [5, 6, 7, 8]);
});
test("prebaked armoury includes native icons/cards for every weapon", () => {
  const pack = JSON.parse(
    readFileSync(new URL("../assets/armoury.json", import.meta.url)),
  );
  const studio = {};
  assert.equal(
    pack.source,
    createHash("sha256")
      .update(readFileSync(new URL("../dist/game.js", import.meta.url)))
      .digest("hex"),
    "Run npm run bake-startup after changing the bundle",
  );
  assert.equal(pack.swatches.length, 10);
  assert.equal(hydrateArmoury(studio, pack), true);
  assert.equal(studio.bakeMs, 0);
  for (const id of ["ar", "smg", "shotgun", "pistol", "frag"]) {
    assert.ok(studio.flat.get(id).sil);
    assert.ok(studio.shaded[id]);
  }
  assert.equal(hydrateArmoury({}, {}), false);
});
