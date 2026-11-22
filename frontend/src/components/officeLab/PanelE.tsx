// EXPERIMENTAL (spike /office-lab, desechable).
// Panel E, "Píxel": pixel art en 3D con Three.js, nítido. La escena (figuras y muebles de bloques, luz en 3 bandas) se
// dibuja en un render target pequeño (NearestFilter, sin mipmaps, sin antialias) con una cámara ortográfica isométrica
// cuyo zoom es un número ENTERO de píxeles por metro; un único pase lo copia al lienzo a escala ENTERA exacta en
// píxeles físicos (cada píxel del dibujo = SCALE×SCALE píxeles de pantalla, contando devicePixelRatio), cuantizando el
// color y trazando un contorno de 1 px oscuro donde salta la profundidad. Sombras duras (BasicShadowMap), ningún
// desenfoque. Las figuras se enganchan a la rejilla de píxeles para que sus bordes no tiemblen. Poses a 6 por segundo y
// un paso de 4 fotogramas, como un sprite.
"use client";
import { useEffect, useMemo, useRef } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { labTime } from "./clock";
import { BubbleProjector, FpsDriver } from "./Bubble3D";
import { PLAN, sample, type Frame } from "./script";
import { poseFor, type PPose, type Role } from "./puppet";

/** Píxeles CSS por píxel de dibujo (por devicePixelRatio y redondeado: siempre entero en píxeles físicos). */
const ART_PX = 3;
const POSE_FPS = 6;
const INK = "#1d1f2b";

// ------------------------------------------------------------------------------------------------ pase final

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
  // contorno de 1 px: este píxel está delante de un vecino por un salto claro de profundidad
  float d0 = z(vUv);
  float e = 0.0;
  e = max(e, z(vUv + vec2(texel.x, 0.0)) - d0);
  e = max(e, z(vUv - vec2(texel.x, 0.0)) - d0);
  e = max(e, z(vUv + vec2(0.0, texel.y)) - d0);
  e = max(e, z(vUv - vec2(0.0, texel.y)) - d0);
  gl_FragColor = vec4(mix(c, ink, step(0.0025, e)), 1.0);
}`;

// ------------------------------------------------------------------------------------------------ materiales (paleta)

const PAL = {
  floor: "#cfd2d6", floor2: "#c0c4ca", wall: "#ece8e1", wall2: "#d9d3c9", wood: "#a98463", woodL: "#c9a57f", dark: "#3a3f4b", grey: "#7d838f",
  navy: "#2b3a67", blue: "#0a66c2", plum: "#6e4256", slate: "#5d6b7d", green: "#4f7a58", greenL: "#6f9a63", pot: "#b5673f", screen: "#cfe6ff", white: "#f4f1ea",
};
let gradient: THREE.DataTexture | null = null;
function toonGradient() {
  if (gradient) return gradient;
  const d = new Uint8Array([150, 150, 150, 255, 205, 205, 205, 255, 255, 255, 255, 255]);
  gradient = new THREE.DataTexture(d, 3, 1, THREE.RGBAFormat);
  gradient.minFilter = gradient.magFilter = THREE.NearestFilter;
  gradient.needsUpdate = true;
  return gradient;
}
const toon = (c: string) => <meshToonMaterial color={c} gradientMap={toonGradient()} />;

function B({ a, p, c, r }: { a: [number, number, number]; p: [number, number, number]; c: string; r?: [number, number, number] }) {
  return <mesh position={p} rotation={r} castShadow receiveShadow><boxGeometry args={a} />{toon(c)}</mesh>;
}

// ------------------------------------------------------------------------------------------------ figuras de bloques

interface Look { skin: string; top: string; bottom: string; hair: string; long?: boolean }
const LOOKS: Record<Role, Look> = {
  agent: { skin: "#c08a63", top: PAL.navy, bottom: PAL.dark, hair: "#2a1c14" },
  partner: { skin: "#7a4b33", top: PAL.slate, bottom: "#30343d", hair: "#17110d", long: true },
  boss: { skin: "#efc6a4", top: PAL.plum, bottom: PAL.dark, hair: "#6b4a33", long: true },
};

/** Ciclo de paso de 4 fotogramas (contacto, paso, contacto, paso) y poses a 6 por segundo. */
function spritePose(role: Role, t: number): { f: Frame; p: PPose } {
  const tq = Math.floor(t * POSE_FPS) / POSE_FPS;
  const f = sample(t); // la posición sigue continua
  const fq = sample(tq);
  if (role === "agent" && f.walking) {
    // el paso no depende del reloj de poses sino de la distancia: 4 fotogramas por zancada
    const frame = Math.floor((f.stride * 3.3) / (Math.PI / 2)) % 4;
    const stride = (frame * Math.PI) / 2 / 3.3 + 0.12;
    return { f, p: poseFor(role, { ...f, stride, speed: 1 }, tq) };
  }
  return { f, p: poseFor(role, { ...fq, pos: f.pos, yaw: f.yaw }, tq) };
}

function Figure({ role }: { role: Role }) {
  const L = LOOKS[role];
  const cam = useThree((st) => st.camera);
  const r = {
    root: useRef<THREE.Group>(null!), body: useRef<THREE.Group>(null!), pelvis: useRef<THREE.Group>(null!), torso: useRef<THREE.Group>(null!), head: useRef<THREE.Group>(null!),
    lSh: useRef<THREE.Group>(null!), rSh: useRef<THREE.Group>(null!), lEl: useRef<THREE.Group>(null!), rEl: useRef<THREE.Group>(null!),
    lHip: useRef<THREE.Group>(null!), rHip: useRef<THREE.Group>(null!), lKnee: useRef<THREE.Group>(null!), rKnee: useRef<THREE.Group>(null!),
    folder: useRef<THREE.Mesh>(null!),
  };
  useFrame(() => {
    const t = labTime();
    const { f, p } = spritePose(role, t);
    const pos = role === "agent" ? f.pos : role === "partner" ? PLAN.partner : PLAN.boss;
    // 8 direcciones, como un sprite
    const rawYaw = role === "agent" ? f.yaw : role === "partner" ? f.partner.yaw : 0;
    const yaw = Math.round(rawYaw / (Math.PI / 4)) * (Math.PI / 4);
    r.root.current.position.set(pos[0], 0, pos[1]);
    snapToPixel(r.root.current.position, cam);
    r.root.current.rotation.y = yaw;
    r.body.current.position.y = p.y;
    r.pelvis.current.position.y = p.pelvis;
    r.torso.current.rotation.set(p.torsoX, p.torsoY, p.torsoZ);
    r.head.current.rotation.set(p.headX, p.headY, p.headZ);
    r.lSh.current.rotation.set(p.lShX, 0, p.lShZ); r.rSh.current.rotation.set(p.rShX, 0, -p.rShZ);
    r.lEl.current.rotation.x = p.lElX; r.rEl.current.rotation.x = p.rElX;
    r.lHip.current.rotation.x = p.lHipX; r.rHip.current.rotation.x = p.rHipX;
    r.lKnee.current.rotation.x = p.lKneeX; r.rKnee.current.rotation.x = p.rKneeX;
    r.folder.current.visible = role === "agent" ? f.folder === "agent" : role === "partner" ? f.partner.mode === "hand" : false;
  });
  return (
    <group ref={r.root}>
      <group ref={r.body}>
        <group ref={r.pelvis}>
          <B a={[0.36, 0.16, 0.22]} p={[0, 0, 0]} c={L.bottom} />
          {([[0.095, r.lHip, r.lKnee], [-0.095, r.rHip, r.rKnee]] as const).map(([x, hip, knee], i) => (
            <group key={i} ref={hip} position={[x, -0.04, 0]}>
              <B a={[0.15, 0.43, 0.17]} p={[0, -0.2, 0]} c={L.bottom} />
              <group ref={knee} position={[0, -0.42, 0]}>
                <B a={[0.14, 0.42, 0.15]} p={[0, -0.2, 0]} c={L.bottom} />
                <B a={[0.15, 0.08, 0.26]} p={[0, -0.43, 0.05]} c="#26221f" />
              </group>
            </group>
          ))}
          <group ref={r.torso}>
            <B a={[0.44, 0.56, 0.25]} p={[0, 0.32, 0]} c={L.top} />
            {role === "agent" && <B a={[0.1, 0.24, 0.02]} p={[0, 0.48, 0.13]} c={PAL.white} />}
            {([[0.28, r.lSh, r.lEl], [-0.28, r.rSh, r.rEl]] as const).map(([x, sh, el], i) => (
              <group key={i} ref={sh} position={[x, 0.55, 0]}>
                <B a={[0.12, 0.3, 0.14]} p={[0, -0.14, 0]} c={L.top} />
                <group ref={el} position={[0, -0.28, 0]}>
                  <B a={[0.11, 0.26, 0.13]} p={[0, -0.12, 0]} c={L.top} />
                  <B a={[0.1, 0.1, 0.11]} p={[0, -0.29, 0]} c={L.skin} />
                  {i === 1 && <mesh ref={r.folder} position={[0, -0.32, 0.12]} rotation={[0.25, 0, 0]} castShadow visible={false}><boxGeometry args={[0.24, 0.3, 0.04]} />{toon(PAL.blue)}</mesh>}
                </group>
              </group>
            ))}
            <group ref={r.head} position={[0, 0.62, 0]}>
              <B a={[0.1, 0.06, 0.1]} p={[0, 0.02, 0]} c={L.skin} />
              <B a={[0.27, 0.29, 0.26]} p={[0, 0.19, 0]} c={L.skin} />
              <B a={[0.29, 0.09, 0.28]} p={[0, 0.35, -0.01]} c={L.hair} />
              <B a={[0.29, 0.2, 0.06]} p={[0, 0.25, -0.13]} c={L.hair} />
              {L.long && <B a={[0.3, 0.3, 0.08]} p={[0, 0.12, -0.14]} c={L.hair} />}
              <B a={[0.045, 0.045, 0.02]} p={[0.06, 0.2, 0.135]} c="#1d1f2b" />
              <B a={[0.045, 0.045, 0.02]} p={[-0.06, 0.2, 0.135]} c="#1d1f2b" />
            </group>
          </group>
        </group>
      </group>
    </group>
  );
}

// ------------------------------------------------------------------------------------------------ oficina de bloques

function Desk({ at, rot = 0 }: { at: [number, number]; rot?: number }) {
  return (
    <group position={[at[0], 0, at[1]]} rotation={[0, rot, 0]}>
      <B a={[1.5, 0.06, 0.75]} p={[0, 0.72, 0]} c={PAL.woodL} />
      <B a={[0.06, 0.7, 0.7]} p={[-0.7, 0.35, 0]} c={PAL.wood} />
      <B a={[0.06, 0.7, 0.7]} p={[0.7, 0.35, 0]} c={PAL.wood} />
      <B a={[1.34, 0.36, 0.04]} p={[0, 0.5, -0.32]} c={PAL.wood} />
    </group>
  );
}
function Chair({ at, rot = 0 }: { at: [number, number]; rot?: number }) {
  return (
    <group position={[at[0], 0, at[1]]} rotation={[0, rot, 0]}>
      <B a={[0.46, 0.08, 0.46]} p={[0, 0.44, 0]} c={PAL.dark} />
      <B a={[0.44, 0.5, 0.07]} p={[0, 0.72, 0.22]} c={PAL.dark} />
      <B a={[0.07, 0.4, 0.07]} p={[0, 0.2, 0]} c={PAL.grey} />
      <B a={[0.5, 0.05, 0.5]} p={[0, 0.03, 0]} c={PAL.grey} />
    </group>
  );
}
function Screen() {
  const mat = useRef<THREE.MeshBasicMaterial>(null!);
  const off = useMemo(() => new THREE.Color(PAL.dark), []), on = useMemo(() => new THREE.Color(PAL.screen), []), hot = useMemo(() => new THREE.Color("#ffffff"), []);
  useFrame(() => {
    const t = Math.floor(labTime() * POSE_FPS) / POSE_FPS, f = sample(t);
    mat.current.color.copy(off).lerp(on, f.screen > 0.5 ? 1 : 0);
    if (f.typing > 0 && Math.floor(t * POSE_FPS) % 2) mat.current.color.lerp(hot, 0.6);
  });
  return (
    <group position={[PLAN.desk.pos[0], 0.75, PLAN.desk.pos[1] - 0.12]}>
      <B a={[0.06, 0.24, 0.06]} p={[0, 0.12, 0]} c={PAL.grey} />
      <B a={[0.7, 0.44, 0.06]} p={[0, 0.44, 0]} c={PAL.dark} />
      <mesh position={[0, 0.44, 0.032]}><planeGeometry args={[0.6, 0.34]} /><meshBasicMaterial ref={mat} color={PAL.dark} /></mesh>
      <B a={[0.46, 0.03, 0.15]} p={[0, 0.015, 0.36]} c={PAL.white} />
    </group>
  );
}
function Plant({ at }: { at: [number, number] }) {
  return (
    <group position={[at[0], 0, at[1]]}>
      <B a={[0.34, 0.36, 0.34]} p={[0, 0.18, 0]} c={PAL.pot} />
      <B a={[0.42, 0.42, 0.42]} p={[0, 0.6, 0]} c={PAL.green} />
      <B a={[0.28, 0.3, 0.28]} p={[0.08, 0.92, 0.04]} c={PAL.greenL} />
    </group>
  );
}
function Loose() {
  const folder = useRef<THREE.Mesh>(null!);
  const check = useRef<THREE.Group>(null!);
  useFrame(() => {
    const t = labTime(), f = sample(t);
    folder.current.visible = (f.folder === "table" && f.partner.mode !== "hand") || f.folder === "boss";
    if (f.folder === "table") folder.current.position.set(PLAN.meeting.pos[0] + 0.15, 0.79, PLAN.meeting.pos[1] - 0.05);
    else folder.current.position.set(PLAN.bossDesk.pos[0] - 0.35, 0.77, PLAN.bossDesk.pos[1] + 0.12);
    // icono de aprobado flotando, que sube y baja a saltos de 2 píxeles como un sprite
    const show = f.approved && t < 23.5;
    check.current.visible = show;
    check.current.position.y = 1.35 + (Math.floor(t * 3) % 2) * 0.06;
  });
  return (
    <>
      <mesh ref={folder} castShadow><boxGeometry args={[0.26, 0.03, 0.34]} />{toon(PAL.blue)}</mesh>
      <group ref={check} position={[PLAN.bossDesk.pos[0] - 0.35, 1.35, PLAN.bossDesk.pos[1] + 0.12]}>
        <B a={[0.36, 0.36, 0.06]} p={[0, 0, 0]} c="#3f8f4f" />
        <B a={[0.08, 0.16, 0.04]} p={[-0.07, -0.02, 0.04]} c={PAL.white} r={[0, 0, 0.8]} />
        <B a={[0.08, 0.26, 0.04]} p={[0.05, 0.03, 0.04]} c={PAL.white} r={[0, 0, -0.7]} />
      </group>
    </>
  );
}

function Office() {
  const { x0, x1, z0, z1 } = PLAN.room;
  const w = x1 - x0, d = z1 - z0;
  const tiles = useMemo(() => {
    const out: [number, number][] = [];
    for (let x = Math.ceil(x0); x + 1 <= x1; x++) for (let z = Math.ceil(z0); z + 1 <= z1; z++) if (Math.abs(x + z) % 2 === 0) out.push([x + 0.5, z + 0.5]);
    return out;
  }, [x0, x1, z0, z1]);
  return (
    <>
      <B a={[w, 0.1, d]} p={[(x0 + x1) / 2, -0.05, (z0 + z1) / 2]} c={PAL.floor} />
      {tiles.map(([x, z], i) => <mesh key={i} position={[x, 0.002, z]} rotation={[-Math.PI / 2, 0, 0]} receiveShadow><planeGeometry args={[1, 1]} />{toon(PAL.floor2)}</mesh>)}
      <B a={[w, 2.2, 0.12]} p={[(x0 + x1) / 2, 1.1, z0 - 0.06]} c={PAL.wall} />
      <B a={[0.12, 2.2, d]} p={[x0 - 0.06, 1.1, (z0 + z1) / 2]} c={PAL.wall2} />
      <B a={[2.2, 0.9, 0.02]} p={[-0.9, 1.2, z0 + 0.01]} c="#9fc0dd" />
      <B a={[w, 0.08, 0.02]} p={[(x0 + x1) / 2, 0.55, z0 + 0.01]} c={PAL.blue} />
      <B a={[0.02, 1.9, 0.9]} p={[x0 + 0.01, 0.95, PLAN.door[1]]} c={PAL.wood} />
      <mesh position={[PLAN.meeting.pos[0], 0.005, PLAN.meeting.pos[1]]} rotation={[-Math.PI / 2, 0, 0]} receiveShadow><planeGeometry args={[2.8, 2.4]} />{toon("#a7b3c2")}</mesh>
      <Desk at={PLAN.desk.pos} />
      <Chair at={PLAN.seat} />
      <Screen />
      <Desk at={PLAN.bossDesk.pos} rot={Math.PI} />
      <Chair at={PLAN.boss} rot={Math.PI} />
      <group position={[PLAN.bossDesk.pos[0] + 0.15, 0.75, PLAN.bossDesk.pos[1] - 0.05]}>
        <B a={[0.44, 0.03, 0.3]} p={[0, 0.015, 0]} c={PAL.grey} />
        <B a={[0.44, 0.3, 0.03]} p={[0, 0.16, -0.14]} c={PAL.grey} />
      </group>
      <group position={[PLAN.meeting.pos[0], 0, PLAN.meeting.pos[1]]}>
        <mesh position={[0, 0.74, 0]} castShadow receiveShadow><cylinderGeometry args={[PLAN.meeting.r, PLAN.meeting.r, 0.06, 8]} />{toon(PAL.woodL)}</mesh>
        <B a={[0.1, 0.72, 0.1]} p={[0, 0.36, 0]} c={PAL.dark} />
      </group>
      <B a={[1.6, 1.5, 0.36]} p={[-0.2, 0.75, -3.4]} c={PAL.wood} />
      {[0, 1, 2, 3, 4, 5].map((i) => <B key={i} a={[0.18, 0.32, 0.26]} p={[-0.8 + i * 0.24, 1.07, -3.33]} c={[PAL.blue, PAL.slate, PAL.white, PAL.plum][i % 4]} />)}
      <Plant at={[-4.7, -3.1]} />
      <Plant at={[4.7, 2.9]} />
      <B a={[1.9, 0.42, 0.8]} p={[-3.2, 0.21, 2.85]} c={PAL.slate} />
      <B a={[1.9, 0.42, 0.2]} p={[-3.2, 0.63, 3.15]} c={PAL.slate} />
      <Loose />
    </>
  );
}

/**
 * El dibujo a baja resolución y su copia a escala entera. Toma el control del render (prioridad 1 en useFrame).
 * Cámara ortográfica propia (manual): 1 unidad del frustum = 1 píxel del dibujo; zoom = píxeles por metro, entero.
 */
function PixelPipeline() {
  const gl = useThree((s) => s.gl);
  const scene = useThree((s) => s.scene);
  const size = useThree((s) => s.size);
  const set = useThree((s) => s.set);
  const pipe = useMemo(() => {
    const cam = new THREE.OrthographicCamera(-1, 1, 1, -1, -40, 60);
    (cam as unknown as { manual: boolean }).manual = true;
    const depth = new THREE.DepthTexture(1, 1);
    depth.minFilter = depth.magFilter = THREE.NearestFilter;
    const rt = new THREE.WebGLRenderTarget(1, 1, { minFilter: THREE.NearestFilter, magFilter: THREE.NearestFilter, generateMipmaps: false, depthBuffer: true, depthTexture: depth, samples: 0 });
    const mat = new THREE.ShaderMaterial({
      vertexShader: VERT, fragmentShader: FRAG, depthTest: false, depthWrite: false,
      uniforms: { tColor: { value: rt.texture }, tDepth: { value: depth }, texel: { value: new THREE.Vector2(1, 1) }, levels: { value: 15 }, ink: { value: new THREE.Color(INK) } },
    });
    const quad = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), mat);
    quad.frustumCulled = false;
    const post = new THREE.Scene();
    post.add(quad);
    return { cam, rt, mat, post, postCam: new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1), bw: 1, bh: 1, scale: 1 };
  }, []);
  // la cámara del panel por defecto (el bocadillo se proyecta con ella)
  useEffect(() => { set({ camera: pipe.cam as unknown as THREE.PerspectiveCamera }); }, [pipe, set]);
  useEffect(() => {
    const dpr = gl.getPixelRatio();
    const scale = Math.max(2, Math.round(ART_PX * dpr)); // píxeles físicos por píxel del dibujo (entero)
    const bw = Math.max(16, Math.floor(Math.floor(size.width * dpr) / scale)), bh = Math.max(16, Math.floor(Math.floor(size.height * dpr) / scale));
    pipe.rt.setSize(bw, bh);
    pipe.mat.uniforms.texel.value.set(1 / bw, 1 / bh);
    const ppu = Math.max(4, Math.floor(Math.min(bw / 12.2, bh / 8.0))); // píxeles por metro (entero)
    const c = pipe.cam;
    c.left = -bw / 2; c.right = bw / 2; c.top = bh / 2; c.bottom = -bh / 2;
    c.zoom = ppu;
    c.position.set(9, 8.4, 9);
    c.lookAt(0, 0.4, -0.1);
    c.updateProjectionMatrix();
    c.updateMatrixWorld();
    pipe.bw = bw; pipe.bh = bh; pipe.scale = scale;
  }, [gl, size, pipe]);
  useEffect(() => () => { pipe.rt.dispose(); pipe.mat.dispose(); }, [pipe]);
  useFrame(() => {
    gl.setRenderTarget(pipe.rt);
    gl.clear();
    gl.render(scene, pipe.cam);
    gl.setRenderTarget(null);
    gl.clear();
    // copia a escala entera, centrada (setViewport va en CSS y three redondea a físicos: pasamos múltiplos exactos)
    const dpr = gl.getPixelRatio();
    const w = pipe.bw * pipe.scale, h = pipe.bh * pipe.scale;
    const ox = Math.floor((Math.floor(size.width * dpr) - w) / 2), oy = Math.floor((Math.floor(size.height * dpr) - h) / 2);
    gl.setViewport(ox / dpr, oy / dpr, w / dpr, h / dpr);
    gl.render(pipe.post, pipe.postCam);
    gl.setViewport(0, 0, size.width, size.height);
  }, 1);
  return null;
}

/** Engancha un punto a la rejilla de píxeles del dibujo (lo mueve lo justo en el plano de la cámara). */
const _r = new THREE.Vector3(), _u = new THREE.Vector3();
function snapToPixel(p: THREE.Vector3, cam: THREE.Camera) {
  const c = cam as THREE.OrthographicCamera;
  if (!c.isOrthographicCamera) return;
  _r.setFromMatrixColumn(c.matrixWorld, 0); _u.setFromMatrixColumn(c.matrixWorld, 1);
  const ppu = c.zoom, sx = p.dot(_r) * ppu, sy = p.dot(_u) * ppu;
  p.addScaledVector(_r, (Math.round(sx) - sx) / ppu).addScaledVector(_u, (Math.round(sy) - sy) / ppu);
}

export default function PanelE({ active }: { active: boolean }) {
  const bubble = useRef<HTMLDivElement>(null);
  const dpr = typeof window !== "undefined" ? window.devicePixelRatio || 1 : 1;
  return (
    <div className="absolute inset-0 overflow-hidden bg-white">
      <Canvas shadows="basic" dpr={dpr} frameloop="demand" gl={{ antialias: false }} style={{ imageRendering: "pixelated" }}>
        <color attach="background" args={["#ffffff"]} />
        <ambientLight intensity={1.5} />
        <hemisphereLight args={["#ffffff", "#b9b3a8", 0.7]} />
        <directionalLight castShadow position={[4, 9, 6]} intensity={2.3} shadow-mapSize={[1024, 1024]} shadow-bias={-0.0015}
          shadow-camera-left={-8} shadow-camera-right={8} shadow-camera-top={7} shadow-camera-bottom={-7} shadow-camera-near={1} shadow-camera-far={30} />
        <PixelPipeline />
        <Office />
        <Figure role="agent" />
        <Figure role="partner" />
        <Figure role="boss" />
        <BubbleProjector el={bubble} standH={2.2} seatH={1.8} step={POSE_FPS} />
        <FpsDriver fps={24} active={active} />
      </Canvas>
      <div ref={bubble} style={{ opacity: 0, fontFamily: "ui-monospace, 'JetBrains Mono', monospace" }} className="pointer-events-none absolute left-0 top-0 whitespace-nowrap border-2 border-[#1d1f2b] bg-white px-2 py-0.5 text-[11px] font-medium text-[#1d1f2b] shadow-[3px_3px_0_#1d1f2b]" />
    </div>
  );
}
