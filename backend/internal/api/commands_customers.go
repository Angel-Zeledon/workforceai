package api

import (
	"fmt"
	"net/http"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/commands"
	"aiworkforce/backend/internal/customers"
)

// /cliente (B12, docs/architecture/customer-district.md): a request for one customer of the firm, typed in the chat.
// The customer goes in ctx and the orchestrator stores the scope before the request exists, exactly like
// POST /requests with customer_id, so its tasks only ever see that customer's memories, files and artifacts.

func init() {
	cmdHandlers["customer"] = handler{run: cmdCustomer, available: func(s *server) bool { return s.Customers != nil }}
}

func cmdCustomer(c *cmdCall) (*cmdResult, *cmdFail) {
	if c.s.Customers == nil {
		return nil, unavailable(c.t("Los clientes", "Customers"))
	}
	cs, err := c.s.Customers.List(c.ctx, false)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	cust, task, f := matchCustomer(c, cs, c.str("customer"), c.str("task"))
	if f != nil {
		return nil, f
	}
	if task == "" {
		return nil, fail(http.StatusBadRequest, "missing_arg", "Escribe qué hay que hacer para el cliente: /cliente <nombre>: <tarea>.",
			"Say what to do for the customer: /customer <name>: <task>.")
	}
	id, err := c.s.Orch.Submit(application.WithCustomer(c.ctx, cust.ID), c.marks()+" "+task)
	if err != nil {
		return nil, c.serviceFail(err)
	}
	return &cmdResult{Kind: "request", RequestID: id, Title: fmt.Sprintf(c.t("Solicitud para %s", "Request for %s"), cust.Name),
		Text: fmt.Sprintf(c.t("Lo puse en marcha para %s: sus tareas solo ven la memoria y los archivos de este cliente.",
			"It is under way for %s: its tasks only see this customer's memory and files."), cust.Name),
		Data: map[string]any{"customer_id": cust.ID}}, nil
}

// matchCustomer finds the active customer a person named: its id, its exact name (ignoring case and accents), the
// longest name the typed words start with ("/cliente Ferretería López concilia..." without a colon), or the one
// customer whose name starts with what was typed. It returns the task text left after the name.
func matchCustomer(c *cmdCall, cs []customers.Customer, name, task string) (customers.Customer, string, *cmdFail) {
	notFound := fail(http.StatusBadRequest, "unknown_customer", fmt.Sprintf("No encuentro al cliente «%s».", name), fmt.Sprintf("I cannot find the customer “%s”.", name))
	if name == "" {
		return customers.Customer{}, "", fail(http.StatusBadRequest, "missing_arg", "Falta el cliente: /cliente <nombre>: <tarea>.", "The customer is missing: /customer <name>: <task>.")
	}
	fn := commands.Fold(name)
	for _, x := range cs {
		if x.ID == name || commands.Fold(x.Name) == fn {
			return x, task, nil
		}
	}
	if task != "" { // a multi-word name typed without a colon
		words := strings.Fields(name + " " + task)
		best, bestN := customers.Customer{}, 0
		for _, x := range cs {
			n := len(strings.Fields(x.Name))
			if n > bestN && n < len(words) && commands.Fold(strings.Join(words[:n], " ")) == commands.Fold(x.Name) {
				best, bestN = x, n
			}
		}
		if bestN > 0 {
			return best, strings.Join(words[bestN:], " "), nil
		}
	}
	var hits []customers.Customer
	for _, x := range cs {
		if strings.HasPrefix(commands.Fold(x.Name), fn) {
			hits = append(hits, x)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], task, nil
	case 0:
		return customers.Customer{}, "", notFound
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.Name)
	}
	list := strings.Join(names, ", ")
	return customers.Customer{}, "", fail(http.StatusBadRequest, "ambiguous_customer", fmt.Sprintf("«%s» puede ser %s: escribe el nombre completo.", name, list),
		fmt.Sprintf("“%s” could be %s: type the full name.", name, list))
}
