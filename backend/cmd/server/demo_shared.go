package main

import (
	"context"
	"log/slog"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

// demoSharedFiles is the business context of the demo company, so agents working
// with a REAL model have documents to read (live finding: with an empty /Shared they
// correctly answered "no data available" to every request). Fictional, coherent with
// the $50,000 Grupo Alfa scenario; only written to the demo organization's /Shared
// and only while it is empty.
var demoSharedFiles = map[string]string{
	"/Shared/Clientes/Grupo Alfa/brief.md": `# Brief: Grupo Alfa (cliente)

Empresa de retail con 42 tiendas en Costa Rica y Panamá. Contacto: Laura Méndez, gerente de transformación digital (decisora).

## Lo que pide
- Rediseño completo de su tienda en línea y app móvil (catálogo de 8.000 productos).
- Integración con su ERP (SAP Business One) para inventario y pedidos.
- Presupuesto aprobado: USD 50.000. Fecha límite de entrega: 8 semanas desde la firma.

## Historial
- Cliente desde 2023; 2 proyectos anteriores entregados a tiempo; paga a 30 días.
- En 2024 pidió un descuento del 8 % que se concedió.

## Riesgos conocidos
- Su equipo de TI es pequeño (2 personas): las pruebas de integración dependen de ellos.
- Temporada alta en diciembre: no quieren cambios en producción después del 20 de noviembre.
`,
	"/Shared/Clientes/Grupo Alfa/borrador-contrato.md": `# Borrador de contrato de servicios: Grupo Alfa

1. Objeto: rediseño de tienda en línea y app, integración con SAP Business One.
2. Precio: USD 50.000, 40 % a la firma, 30 % a la entrega de la beta, 30 % a la puesta en producción.
3. Plazo: 8 semanas desde la firma.
4. Penalidad por atraso: 2 % del valor total por semana, sin tope.
5. Cambios de alcance: no se menciona procedimiento.
6. Propiedad intelectual: el código queda a nombre del cliente al pago total.
7. Garantía: 90 días sobre defectos.
8. Confidencialidad: 3 años.
`,
	"/Shared/Finanzas/tarifas-y-costos.csv": `rol,costo_hora_usd,tarifa_hora_usd
diseñador_ux,28,55
desarrollador_frontend,32,65
desarrollador_backend,35,70
integrador_sap,45,95
qa,22,45
project_manager,38,75
`,
	"/Shared/Operaciones/capacidad-equipo.csv": `semana,equipo,capacidad_horas,comprometido_horas
1,diseño,80,40
1,desarrollo,240,180
1,integracion,40,40
2,diseño,80,30
2,desarrollo,240,200
2,integracion,40,40
3,diseño,80,20
3,desarrollo,240,160
3,integracion,40,20
4,diseño,80,20
4,desarrollo,240,120
4,integracion,40,0
`,
	"/Shared/Politicas/politica-de-descuentos.md": `# Política de descuentos

- Hasta 10 % sin aprobación adicional.
- Más de 10 % requiere aprobación de Dirección.
- Margen mínimo aceptable después de descuentos: 25 %.
`,
}

// seedDemoShared writes the demo company's documents when the demo org's /Shared is empty.
func seedDemoShared(ctx context.Context, svc *drive.Service, demoOrg string, log *slog.Logger) {
	if svc == nil || demoOrg == "" {
		return
	}
	ctx = application.WithOrg(ctx, demoOrg)
	if l, err := svc.List(ctx, "/Shared"); err != nil || len(l.Files) > 0 || len(l.Folders) > 0 {
		return
	}
	n := 0
	for path, content := range demoSharedFiles {
		if _, err := svc.Write(ctx, drive.System(), drive.WriteInput{Path: path, Data: []byte(content), Message: "demo company documents", Source: "demo_seed"}); err == nil {
			n++
		}
	}
	log.Info("demo company documents seeded in /Shared", "files", n)
}
