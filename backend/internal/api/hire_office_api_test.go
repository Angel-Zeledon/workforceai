package api_test

import (
	"strings"
	"testing"
)

func (p *posEnv) agentCount() int { return len(p.e.agentIDs(p.owner.AccessToken)) }

func (p *posEnv) hireStatus(body map[string]any) int {
	return p.e.status("POST", api1+"/agents/from-template", p.owner.AccessToken, body)
}

func TestHireWithOffice(t *testing.T) {
	p := newPosEnv(t)
	legal, privada := p.office("Legal"), p.office("Privada")
	tok := p.owner.AccessToken
	skill := ""
	for _, s := range p.e.list(api1+"/skills", tok) {
		if s["status"] == "active" && s["office_id"] == "" {
			skill = s["id"].(string)
			break
		}
	}
	pos := p.e.json("POST", api1+"/offices/"+legal+"/positions", p.ana, map[string]any{"name": "Analista", "skills": []string{}}, 201)["id"].(string)
	other := p.e.json("POST", api1+"/offices/"+p.office("Finanzas")+"/positions", tok, map[string]any{"name": "Contador"}, 201)["id"].(string)
	before := p.agentCount()

	// everything is checked before the agent exists: a refused hire leaves nothing behind
	bads := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown office", map[string]any{"template_id": "data_analyst", "office_id": "nowhere"}, 404},
		{"private office the admin is not in", map[string]any{"template_id": "data_analyst", "office_id": privada}, 404},
		{"unknown position", map[string]any{"template_id": "data_analyst", "office_id": legal, "position_id": "nope"}, 404},
		{"position of another office", map[string]any{"template_id": "data_analyst", "office_id": legal, "position_id": other}, 400},
		{"position without office", map[string]any{"template_id": "data_analyst", "position_id": pos}, 400},
		{"unknown skill", map[string]any{"template_id": "data_analyst", "office_id": legal, "position_id": pos, "skills": []string{skill, "nope"}}, 404},
		{"bad visibility", map[string]any{"template_id": "data_analyst", "office_id": legal, "visibility": "everyone"}, 400},
		{"share with a stranger", map[string]any{"template_id": "data_analyst", "office_id": legal, "shares": []map[string]any{{"user_id": "u-nobody"}}}, 400},
	}
	for _, b := range bads {
		if c := p.hireStatus(b.body); c != b.want {
			t.Fatalf("%s = %d, want %d", b.name, c, b.want)
		}
		if n := p.agentCount(); n != before {
			t.Fatalf("%s left an agent behind (%d -> %d)", b.name, before, n)
		}
	}
	// a member without the right to hire is refused whatever the office
	if c := p.e.status("POST", api1+"/agents/from-template", p.ana, map[string]any{"template_id": "data_analyst", "office_id": legal}); c != 403 {
		t.Fatalf("member hiring for her office = %d", c)
	}

	// the whole hire in one call
	res := p.e.json("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "data_analyst", "office_id": legal,
		"position_id": pos, "skills": []string{skill}, "visibility": "office"}, 201)
	id := res["agent"].(map[string]any)["id"].(string)
	acc := p.e.json("GET", api1+"/agents/"+id+"/access", tok, nil, 200)
	if acc["office_id"] != legal || acc["office_name"] != "Legal" || acc["visibility"] != "office" || acc["owner_kind"] != "office" {
		t.Fatalf("access = %v", acc)
	}
	if got := p.e.json("GET", api1+"/agents/"+id+"/position", tok, nil, 200); got["position"].(map[string]any)["id"] != pos {
		t.Fatalf("position = %v", got)
	}
	if sk := mustJSON(t, p.e.list(api1+"/agents/"+id+"/skills", tok)); !strings.Contains(sk, skill) {
		t.Fatalf("skills = %s", sk)
	}
	if !strings.Contains(mustJSON(t, p.e.json("GET", api1+"/org/chart", tok, nil, 200)), `"id":"`+id+`"`) {
		t.Fatal("the hired agent is not in the chart")
	}
	if v := p.e.json("GET", api1+"/agents/"+id+"/offices", tok, nil, 200); v["base_office"].(map[string]any)["id"] != legal {
		t.Fatalf("base office = %v", v)
	}

	// without office_id: General, as before
	g := p.e.json("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "education"}, 201)["agent"].(map[string]any)["id"].(string)
	if acc := p.e.json("GET", api1+"/agents/"+g+"/access", tok, nil, 200); acc["office_id"] != "general" || acc["explicit"] != false {
		t.Fatalf("general hire = %v", acc)
	}
}

func TestSuggestedSkills(t *testing.T) {
	p := newPosEnv(t)
	legal := p.office("Legal")
	tok := p.owner.AccessToken
	skill, name := "", ""
	for _, s := range p.e.list(api1+"/skills", tok) {
		if s["status"] == "active" && s["office_id"] == "" {
			skill, name = s["id"].(string), s["name"].(string)
			break
		}
	}
	pos := p.e.json("POST", api1+"/offices/"+legal+"/positions", tok, map[string]any{"name": "Analista", "skills": []string{skill}}, 201)["id"].(string)
	got := p.e.json("GET", api1+"/role-templates/data_analyst/suggested-skills?office_id="+legal+"&position_id="+pos, tok, nil, 200)
	sk := got["skills"].([]any)
	if len(sk) == 0 || sk[0].(map[string]any)["id"] != skill || sk[0].(map[string]any)["via"] != "position" || sk[0].(map[string]any)["name"] != name {
		t.Fatalf("suggested = %v", got)
	}
	if got["position"].(map[string]any)["id"] != pos {
		t.Fatalf("position = %v", got)
	}
	p.e.json("GET", api1+"/role-templates/nope/suggested-skills", tok, nil, 404)
	p.e.json("GET", api1+"/role-templates/data_analyst/suggested-skills?position_id="+pos, tok, nil, 400)
	p.e.json("GET", api1+"/role-templates/data_analyst/suggested-skills", tok, nil, 200)
}
