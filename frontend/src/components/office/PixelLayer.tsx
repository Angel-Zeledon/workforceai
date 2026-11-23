"use client";
import { useEffect, useMemo } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";

/**
 * Pixel style: the real office drawn as pixel art. Everything lives in this one pass:
 *  - the scene is drawn into a SMALL render target (NearestFilter, no mipmaps, no antialiasing) with an orthographic
 *    camera whose zoom is a whole number of art pixels per meter;
 *  - one quad copies it to the canvas at an exact WHOLE-number scale in physical pixels (device pixel ratio counted:
 *    every art pixel is S x S screen pixels), quantizing the color to a few levels and drawing a 1 px dark outline where
 *    the depth jumps;
 *  - the orbit camera keeps its own (continuous) zoom; it is snapped here to a whole number of art pixels per meter, so
 *    clicks, labels and the picture agree.
 * It takes over the frame's render (useFrame priority 1), like a post-processing composer does.
 */

/** CSS pixels per art pixel at a device pixel ratio of 1 (the scale is rounded so it is always whole in physical pixels). */
export const ART_PX = 3;
/** On a short canvas the art pixels are finer, so the whole office still fits with some detail. */
const artPx = (cssHeight: number) => (cssHeight < 760 ? 2 : ART_PX);
const INK = "#1d1f2b";
const MIN_PPU = 6, MAX_PPU = 90;

const VERT = /* glsl */ `varying vec2 vUv; void main() { vUv = uv; gl_Position = vec4(position.xy, 0.0, 1.0); }`;
const FRAG = /* glsl */ `
uniform sampler2D tColor;
uniform sampler2D tDepth;
uniform vec2 texel;
uniform float levels;
uniform vec3 ink;
varying vec2 vUv;
float z(vec2 uv) { return texture2D(tDepth, uv).x; }
void main() {
  vec3 lin = texture2D(tColor, vUv).rgb;
  vec3 c = pow(max(lin, 0.0), vec3(1.0 / 2.2));
  c = floor(c * levels + 0.5) / levels;
  // 1 px outline: this pixel is in front of a neighbour by a clear jump in depth
  float d0 = z(vUv);
  float e = 0.0;
  e = max(e, z(vUv + vec2(texel.x, 0.0)) - d0);
  e = max(e, z(vUv - vec2(texel.x, 0.0)) - d0);
  e = max(e, z(vUv + vec2(0.0, texel.y)) - d0);
  e = max(e, z(vUv - vec2(0.0, texel.y)) - d0);
  gl_FragColor = vec4(mix(c, ink, step(0.003, e)), 1.0);
}`;

/** Physical pixels per art pixel (a whole number, at least 2). */
export const artScale = (dpr: number, cssHeight: number) => Math.max(2, Math.round(artPx(cssHeight) * dpr));

const _r = new THREE.Vector3(), _u = new THREE.Vector3();

export function PixelLayer() {
  const gl = useThree((s) => s.gl);
  const scene = useThree((s) => s.scene);
  const size = useThree((s) => s.size);
  const pipe = useMemo(() => {
    const cam = new THREE.OrthographicCamera(-1, 1, 1, -1, 0.1, 90);
    (cam as unknown as { manual: boolean }).manual = true;
    const depth = new THREE.DepthTexture(1, 1);
    depth.minFilter = depth.magFilter = THREE.NearestFilter;
    const rt = new THREE.WebGLRenderTarget(1, 1, { minFilter: THREE.NearestFilter, magFilter: THREE.NearestFilter, generateMipmaps: false, depthBuffer: true, depthTexture: depth, samples: 0 });
    const mat = new THREE.ShaderMaterial({
      vertexShader: VERT, fragmentShader: FRAG, depthTest: false, depthWrite: false,
      uniforms: { tColor: { value: rt.texture }, tDepth: { value: depth }, texel: { value: new THREE.Vector2(1, 1) }, levels: { value: 14 }, ink: { value: new THREE.Color(INK) } },
    });
    const quad = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), mat);
    quad.frustumCulled = false;
    const post = new THREE.Scene();
    post.add(quad);
    return { cam, rt, mat, post, postCam: new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1), bw: 1, bh: 1, scale: 1, ppu: 0 };
  }, []);

  // the size of the small picture follows the canvas
  useEffect(() => {
    const dpr = gl.getPixelRatio();
    const scale = artScale(dpr, size.height);
    const bw = Math.max(16, Math.floor(Math.floor(size.width * dpr) / scale)), bh = Math.max(16, Math.floor(Math.floor(size.height * dpr) / scale));
    pipe.rt.setSize(bw, bh);
    pipe.mat.uniforms.texel.value.set(1 / bw, 1 / bh);
    const c = pipe.cam;
    c.left = -bw / 2; c.right = bw / 2; c.top = bh / 2; c.bottom = -bh / 2;
    pipe.bw = bw; pipe.bh = bh; pipe.scale = scale;
  }, [gl, size, pipe]);
  useEffect(() => () => { pipe.rt.dispose(); pipe.mat.dispose(); }, [pipe]);

  useFrame(({ camera }) => {
    const live = camera as THREE.OrthographicCamera;
    if (!live.isOrthographicCamera) return;
    const dpr = gl.getPixelRatio();
    // first frame: frame the whole floor plan for this canvas (CSS pixels per meter)
    if (!pipe.ppu) live.zoom = Math.max(10, Math.min(size.width / 34, size.height / 21));
    // the orbit camera's zoom (CSS pixels per meter) becomes a whole number of art pixels per meter; a wheel step always moves it
    const cssPerArt = pipe.scale / dpr;
    const raw = live.zoom / cssPerArt;
    let ppu = pipe.ppu || Math.round(raw);
    if (raw > ppu + 0.05) ppu = Math.ceil(raw - 0.05);
    else if (raw < ppu - 0.05) ppu = Math.floor(raw + 0.05);
    ppu = Math.min(MAX_PPU, Math.max(MIN_PPU, ppu));
    pipe.ppu = ppu;
    const zoom = ppu * cssPerArt;
    if (live.zoom !== zoom) { live.zoom = zoom; live.updateProjectionMatrix(); }

    // the drawing camera: same place and direction, frustum in art pixels, positioned on the pixel grid
    const c = pipe.cam;
    live.updateMatrixWorld();
    c.position.copy(live.position);
    c.quaternion.copy(live.quaternion);
    c.zoom = ppu;
    c.updateMatrixWorld();
    _r.setFromMatrixColumn(c.matrixWorld, 0); _u.setFromMatrixColumn(c.matrixWorld, 1);
    const sx = c.position.dot(_r) * ppu, sy = c.position.dot(_u) * ppu;
    c.position.addScaledVector(_r, (Math.round(sx) - sx) / ppu).addScaledVector(_u, (Math.round(sy) - sy) / ppu);
    c.updateProjectionMatrix();
    c.updateMatrixWorld();

    gl.setRenderTarget(pipe.rt);
    gl.clear();
    gl.render(scene, c);
    gl.setRenderTarget(null);
    gl.clear();
    // copy at the exact whole-number scale, centered (the viewport is in CSS pixels; three rounds to physical ones)
    const w = pipe.bw * pipe.scale, h = pipe.bh * pipe.scale;
    const ox = Math.floor((Math.floor(size.width * dpr) - w) / 2), oy = Math.floor((Math.floor(size.height * dpr) - h) / 2);
    gl.setViewport(ox / dpr, oy / dpr, w / dpr, h / dpr);
    gl.render(pipe.post, pipe.postCam);
    gl.setViewport(0, 0, size.width, size.height);
  }, 1);
  return null;
}
