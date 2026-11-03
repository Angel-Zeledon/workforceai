# Office style assets: origin and license

Used only by the 3D office styles (docs/architecture/office-editor.md, "Styles"). They are downloaded by the browser
only while a style that uses them is active (the HDRI in "bricks" and "real", the texture sets in "real"). Every file
is CC0 1.0 (public domain dedication): no attribution required, credited here anyway.

All files were repacked for the office (scripted, nothing hand-painted): each `pbr/<kind>.jpg` holds the source's
OpenGL normal map in R and G and a high-passed, mean-normalized luminance detail in B (the catalog color is
multiplied by it, so the mean color never changes); `pbr/<kind>_r.jpg` is the source roughness divided by its mean.
The HDRI was downsampled to 512 x 256, desaturated by 85 % (reflections never tint the office) and normalized.

| file | source | author | license |
|---|---|---|---|
| `pbr/wood.jpg`, `pbr/wood_r.jpg` | Poly Haven, "Oak Veneer 01" (oak_veneer_01, 1k) https://polyhaven.com/a/oak_veneer_01 | Jenelle van Heerden | CC0 |
| `pbr/concrete.jpg`, `pbr/concrete_r.jpg` | Poly Haven, "Painted Concrete 02" (painted_concrete_02, 1k) https://polyhaven.com/a/painted_concrete_02 | Rob Tuytel | CC0 |
| `pbr/plaster.jpg`, `pbr/plaster_r.jpg` | Poly Haven, "Painted Plaster Wall" (painted_plaster_wall, 1k) https://polyhaven.com/a/painted_plaster_wall | Amal Kumar | CC0 |
| `pbr/fabric.jpg`, `pbr/fabric_r.jpg` | ambientCG, "Fabric 082 A" (Fabric082A, 1K-JPG) https://ambientcg.com/view?id=Fabric082A | ambientCG | CC0 |
| `pbr/metal.jpg`, `pbr/metal_r.jpg` | ambientCG, "Metal 009" (Metal009, 1K-JPG, brushed) https://ambientcg.com/view?id=Metal009 | ambientCG | CC0 |
| `env/room_512.hdr` | Poly Haven, "Photo Studio Loft Hall" (photo_studio_loft_hall, 1k HDR) https://polyhaven.com/a/photo_studio_loft_hall | Sergej Majboroda | CC0 |

Total: about 1.1 MB.
