const DB_NAME = "golden-hour-startup";
const DB_VERSION = 1;
const MAX_TEXTURE_BYTES = 160 * 1024 * 1024;
const PROPERTIES = [
  "mapping",
  "channel",
  "wrapS",
  "wrapT",
  "magFilter",
  "minFilter",
  "format",
  "type",
  "internalFormat",
  "colorSpace",
  "flipY",
  "premultiplyAlpha",
  "unpackAlignment",
  "generateMipmaps",
  "anisotropy",
  "rotation",
  "matrixAutoUpdate",
];

// Cache pixel data, not WebGL objects or shader binaries: those belong to a
// particular GPU/context. No network or room state is stored in this database.
export function captureMaterials(tree) {
  const textures = [],
    seen = new Map();
  let bytes = 0;
  function visit(value) {
    if (value?.isTexture) {
      if (seen.has(value)) return { texture: seen.get(value) };
      const image = value.image;
      if (!image?.width || !image?.height)
        throw Error("Unsupported cached image");
      let data = image.data;
      if (!data)
        data = image
          .getContext("2d", { willReadFrequently: true })
          .getImageData(0, 0, image.width, image.height).data;
      if (!(data instanceof Uint8Array || data instanceof Uint8ClampedArray))
        throw Error("Unsupported cached pixels");
      bytes += data.byteLength;
      if (bytes > MAX_TEXTURE_BYTES)
        throw Error("Material cache budget exceeded");
      const record = {
        data,
        canvas: !value.isDataTexture,
        width: image.width,
        height: image.height,
        properties: {},
        repeat: value.repeat.toArray(),
        offset: value.offset.toArray(),
        center: value.center.toArray(),
        userData: value.userData,
      };
      for (const key of PROPERTIES) record.properties[key] = value[key];
      const index = textures.length;
      seen.set(value, index);
      textures.push(record);
      return { texture: index };
    }
    if (Array.isArray(value)) return value.map(visit);
    if (value && typeof value === "object") {
      const out = {};
      for (const [key, child] of Object.entries(value)) {
        if (key !== "timings") out[key] = visit(child);
      }
      return out;
    }
    return value;
  }
  const result = { schema: 1, tree: visit(tree), textures, bytes };
  return result;
}

export function restoreMaterials(pack, create, anisotropy) {
  if (
    pack?.schema !== 1 ||
    !pack.tree ||
    !Array.isArray(pack.textures) ||
    !pack.textures.length ||
    pack.bytes > MAX_TEXTURE_BYTES
  )
    return null;
  let totalBytes = 0;
  const textures = pack.textures.map((record) => {
    if (
      !Number.isInteger(record.width) ||
      !Number.isInteger(record.height) ||
      record.width < 1 ||
      record.height < 1 ||
      record.width > 8192 ||
      record.height > 8192 ||
      !record.properties ||
      !(
        record.data instanceof Uint8Array ||
        record.data instanceof Uint8ClampedArray
      ) ||
      record.data?.byteLength !== record.width * record.height * 4
    )
      throw Error("Invalid material cache");
    totalBytes += record.data.byteLength;
    if (totalBytes > MAX_TEXTURE_BYTES)
      throw Error("Material cache budget exceeded");
    const texture = create(record);
    for (const key of PROPERTIES) texture[key] = record.properties[key];
    texture.repeat.fromArray(record.repeat);
    texture.offset.fromArray(record.offset);
    texture.center.fromArray(record.center);
    texture.userData = record.userData || {};
    texture.anisotropy = Math.min(anisotropy, record.properties.anisotropy ?? 8);
    texture.needsUpdate = true;
    return texture;
  });
  function visit(value) {
    if (
      value &&
      typeof value === "object" &&
      Object.keys(value).length === 1 &&
      Number.isInteger(value.texture)
    ) {
      if (!textures[value.texture]) throw Error("Invalid texture reference");
      return textures[value.texture];
    }
    if (Array.isArray(value)) return value.map(visit);
    if (value && typeof value === "object")
      return Object.fromEntries(
        Object.entries(value).map(([key, child]) => [key, visit(child)]),
      );
    return value;
  }
  return { ...visit(pack.tree), timings: { cached: 1 } };
}

function database() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => request.result.createObjectStore("cache");
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
    request.onblocked = () => reject(Error("Startup cache locked"));
  });
}
export async function readCache(key) {
  const db = await database();
  try {
    return await new Promise((resolve, reject) => {
      const request = db.transaction("cache").objectStore("cache").get(key);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
  } finally {
    db.close();
  }
}
export async function writeCache(key, value) {
  const db = await database();
  try {
    await new Promise((resolve, reject) => {
      const tx = db.transaction("cache", "readwrite"),
        store = tx.objectStore("cache");
      // Derived data from an older bundle is disposable; bound disk usage.
      store.clear();
      store.put(value, key);
      tx.oncomplete = resolve;
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error);
    });
  } finally {
    db.close();
  }
}

export function hydrateArmoury(studio, pack) {
  if (
    pack?.schema !== 1 ||
    !Array.isArray(pack.flat) ||
    !Array.isArray(pack.cards)
  )
    return false;
  const flat = new Map(pack.flat),
    cards = new Map(pack.cards);
  for (const key of ["frag", "ar", "smg", "shotgun", "pistol"]) {
    const icon = flat.get(key);
    if (
      !icon ||
      !/^data:image\/(png|webp);base64,/.test(icon.sil) ||
      !/^data:image\/(png|webp);base64,/.test(icon.line) ||
      !Number.isFinite(icon.aspect) ||
      !/^data:image\/(png|webp);base64,/.test(pack.shaded?.[key])
    )
      return false;
  }
  studio.flat = flat;
  studio.cards = cards;
  studio.shaded = { ...pack.shaded };
  studio.bakeMs = 0;
  return true;
}
