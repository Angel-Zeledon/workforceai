package domain

// SeedOrg is the demo organization.
func SeedOrg(budget float64) Organization {
	return Organization{ID: DemoOrgID, Name: "Demo", Slug: "demo", BudgetUSD: budget}
}

// SeedAgents returns the 7 agents defined by the SPEC.
func SeedAgents() []Agent {
	mk := func(id, name, title, desc, persona string, resp, tools, perms []string) Agent {
		return Agent{
			ID: id, Name: name, Role: id, Title: title, Description: desc,
			State: StateIdle, Activity: "Disponible",
			Tools: tools, Permissions: perms, Autonomy: "rules",
			Persona: persona, Responsibilities: resp,
		}
	}
	return []Agent{
		mk("sales", "Valeria Ríos", "Gerente de Ventas",
			"Gestiona clientes, propuestas y el pipeline comercial.",
			"Gerente de ventas orientada a resultados, cercana al cliente y rigurosa con los números.",
			[]string{"Análisis de clientes", "Preparación de propuestas", "Seguimiento comercial"},
			[]string{"crm", "email", "proposal_builder"}, []string{"read:crm", "send:proposal"}),
		mk("hr", "Marcos Peña", "Recursos Humanos",
			"Contratación, onboarding y clima laboral.",
			"Profesional de RRHH empático, cuidadoso con la normativa laboral.",
			[]string{"Reclutamiento", "Onboarding", "Políticas internas"},
			[]string{"ats", "email"}, []string{"read:hr", "create:job_post"}),
		mk("legal", "Elena Castro", "Abogada",
			"Revisión de contratos, cumplimiento y riesgos legales.",
			"Abogada meticulosa y prudente; señala riesgos con claridad.",
			[]string{"Revisión de contratos", "Cumplimiento", "Riesgo legal"},
			[]string{"contract_review", "docs"}, []string{"read:contracts", "send:contract"}),
		mk("accounting", "Tomás Vidal", "Contador",
			"Márgenes, costos, facturación y control financiero.",
			"Contador exacto y escéptico; no da por buenos números sin verificar.",
			[]string{"Márgenes y costos", "Facturación", "Presupuestos"},
			[]string{"ledger", "spreadsheet"}, []string{"read:finance"}),
		mk("analyst", "Nadia Ortega", "Analista",
			"Análisis de datos, rentabilidad y métricas del negocio.",
			"Analista curiosa que formula hipótesis y las contrasta con evidencia.",
			[]string{"Análisis de rentabilidad", "Métricas", "Hipótesis y evidencia"},
			[]string{"bi", "spreadsheet"}, []string{"read:analytics"}),
		mk("operations", "Iván Duarte", "Operaciones",
			"Capacidad operativa, logística y entrega.",
			"Responsable de operaciones pragmático, piensa en capacidad y plazos.",
			[]string{"Capacidad", "Planificación de entrega", "Proveedores"},
			[]string{"planner", "inventory"}, []string{"read:operations"}),
		mk("assistant", "Sofía Lara", "Secretaria / Asistente Ejecutivo",
			"Recibe solicitudes, coordina al equipo y consolida resultados.",
			"Asistente ejecutiva organizada y clara; coordina y resume para la dirección.",
			[]string{"Coordinación", "Planificación de solicitudes", "Informes ejecutivos"},
			[]string{"calendar", "docs"}, []string{"read:all"}),
	}
}
