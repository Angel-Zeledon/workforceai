"""Scripted content (English) for SimulationEngine. Mirrors sim_content.py (Spanish, default)."""
from __future__ import annotations

PLANS: dict[str, dict] = {
    "new_client": {
        "objectives": [
            "Evaluate and close the $50,000 proposal for the new client",
            "Validate real profitability and legal risks before sending the proposal",
            "Confirm that Operations can deliver with current capacity",
        ],
        "tasks": [
            ("ventas", "sales", "Analyze the client and prepare the proposal",
             "Analyze the new client (industry, size, needs) and prepare the $50,000 commercial proposal. The proposal is not sent without approval.", []),
            ("legal", "legal", "Review contract and clauses",
             "Review the draft contract tied to the proposal: penalties, intellectual property, termination and liability.", ["ventas"]),
            ("contador", "accounting", "Calculate the real margin of the proposal",
             "Calculate the real margin considering direct costs, commissions and payment terms; compare it with the budgeted 31%.", ["ventas"]),
            ("analista", "analyst", "Analyze client profitability",
             "Analyze expected profitability and client concentration risk using the Accountant's real margin.", ["ventas", "contador"]),
            ("operaciones", "operations", "Assess operational capacity",
             "Assess whether the current team can meet the proposal's scope and deadlines; estimate staffing needs.", ["ventas"]),
        ],
        "questions": [],
    },
    "sales_drop": {
        "objectives": ["Understand why sales dropped", "Identify commercial and financial recovery actions"],
        "tasks": [
            ("analista", "analyst", "Diagnose the sales drop",
             "Analyze the sales series by product, channel and segment to identify the source of the drop.", []),
            ("ventas", "sales", "Review pipeline and conversion",
             "Review the pipeline, conversion rates and loss reasons in light of the Analyst's diagnosis.", ["analista"]),
            ("contador", "accounting", "Measure cash flow impact",
             "Quantify the impact of the drop on revenue, margin and cash flow for the next 90 days.", ["analista"]),
        ],
        "questions": ["Since what date or period have you noticed the drop?"],
    },
    "hiring": {
        "objectives": ["Define the hiring profile and process", "Validate total cost and legal compliance"],
        "tasks": [
            ("rrhh", "hr", "Define profile and recruiting process",
             "Define the profile, salary band, recruiting channels and interview schedule.", []),
            ("contador", "accounting", "Estimate total hiring cost",
             "Estimate total monthly and annual cost (salary, payroll charges, equipment) and its effect on the budget.", ["rrhh"]),
            ("legal", "legal", "Review employment contract and compliance",
             "Prepare and review the employment contract, probation period and legal obligations.", ["rrhh"]),
        ],
        "questions": ["What is the exact position and target salary?"],
    },
    "contract": {
        "objectives": ["Review the contract and its risks", "Align the commercial stance with the legal review"],
        "tasks": [
            ("legal", "legal", "Review the contract",
             "Review clauses, risks, penalties and obligations of the indicated contract.", []),
            ("ventas", "sales", "Align commercial terms",
             "Align commercial terms with the legal findings and prepare the counteroffer.", ["legal"]),
        ],
        "questions": [],
    },
    "default": {
        "objectives": ["Understand and handle the request"],
        "tasks": [
            ("asistente", "assistant", "Structure the request",
             "Clarify the scope of the request, gather context and define deliverables.", []),
            ("analista", "analyst", "Analyze the available information",
             "Analyze the available data and propose actionable conclusions.", ["asistente"]),
        ],
        "questions": ["Is there a deadline or a specific outcome you need?"],
    },
}


def _o(summary, findings, metrics, hypotheses, evidence, recommendations, confidence, suggested):
    return dict(summary=summary, findings=findings, metrics=metrics, hypotheses=hypotheses,
                evidence=evidence, recommendations=recommendations, confidence=confidence,
                suggested_tasks=suggested)


CONTENT: dict[tuple[str, str], dict] = {}

CONTENT[("new_client", "sales")] = dict(
    output=_o(
        "The client fits our ideal profile (mid-size logistics company, 180 employees) and the $50,000 proposal is ready, but it needs approval before sending because it commits price and timelines.",
        ["Mid-size logistics client with an approved budget for the quarter.",
         "Needs implementation in 10 weeks; competitor B offered a 12% lower price with no support included.",
         "The decision maker (Director of Operations) values support SLA over price.",
         "Proposal prepared: scope, timeline, 3 payment milestones (40/30/30) and a 99.5% SLA."],
        {"proposal_value_usd": 50000, "close_probability": "68%", "estimated_cycle_days": 21, "discount_offered": "5%"},
        ["If sent this week the close probability rises to ~75%.", "The 5% discount may be unnecessary given the interest in the SLA."],
        ["CRM: 3 prior meetings with the client, the last one 4 days ago.", "Client brief: quarterly budget approved."],
        ["Send the proposal once margin and contract are validated (see Accountant and Legal).",
         "Keep the discount at 5% at most and make it conditional on prepayment of the first milestone."],
        0.78,
        ["Schedule a closing call with the Director of Operations", "Prepare a case study from a similar logistics client"],
    ),
    consults=[],
    tool_requests=[dict(tool="email", action="send_proposal", risk="high",
                        args=dict(to="directora.operaciones@clientenuevo.example", subject="Commercial proposal - $50,000",
                                  attachment="propuesta_cliente_nuevo_v1.pdf", amount_usd=50000))],
)
CONTENT[("new_client", "legal")] = dict(
    output=_o(
        "The draft contract is acceptable with three adjustments: cap the late-delivery penalty, clarify intellectual property and limit total liability.",
        ["The late-delivery penalty (1% weekly, uncapped) is excessive; recommend a cap of 10% of the value.",
         "The IP clause assigns all code to the client, including our own reusable components.",
         "Liability is not limited; it should be capped at the contract amount.",
         "Termination for convenience at 15 days is short; propose 30 days."],
        {"clauses_reviewed": 24, "high_risks": 1, "medium_risks": 2, "proposed_changes": 4},
        ["The client will accept the penalty cap if the SLA is kept.", "A data protection annex is required if personal data is involved."],
        ["Draft contract v0.3, clauses 7, 11, 14 and 18.", "Company standard template in force for 2026."],
        ["Do not sign without a penalty cap and a liability limit.", "Include an IP annex with a carve-out for our own components."],
        0.84,
        ["Send the contract redline to Sales", "Prepare the data protection annex"],
    ),
    consults=[dict(to_agent_id="sales", question="Has the client already accepted the 40/30/30 payment schedule, or is it still under negotiation?")],
    tool_requests=[],
)
CONTENT[("new_client", "accounting")] = dict(
    output=_o(
        "The budgeted 31% margin does not hold: with real costs, commissions and the 5% discount, the real margin is 24%, below the 28% target.",
        ["Budgeted gross margin: 31% ($15,500 on $50,000).",
         "Estimated real margin: 24% ($12,000) after implementation costs, sales commission (4%) and discount.",
         "A gap of 7 percentage points ($3,500) versus budget.",
         "The 40/30/30 payment scheme leaves the final 30% at 75 days, affecting working capital."],
        {"budgeted_margin": "31%", "real_margin": "24%", "gap_pp": 7, "direct_cost_usd": 38000, "revenue_usd": 50000},
        ["Raising the upfront payment to 50% would improve cash flow without affecting margin.", "Removing the discount recovers ~2.5 margin points."],
        ["Project cost sheet v2.", "Current commission table.", "Payment terms of the draft proposal."],
        ["Remove the 5% discount or offset it with a smaller scope.", "Renegotiate payments to 50/25/25.", "Do not approve below a 26% margin without management authorization."],
        0.88,
        ["Recalculate margin with a 50% upfront payment", "Update the quarterly budget"],
    ),
    consults=[dict(to_agent_id="sales", question="What discount and payment terms were offered to the client in the proposal?")],
    tool_requests=[],
)
CONTENT[("new_client", "analyst")] = dict(
    output=_o(
        "The client is profitable but marginal: with a real margin of 24% the return on hours is 18% below the portfolio average, and it would represent 14% of quarterly billing.",
        ["Estimated profitability per hour: $118 versus a $144 portfolio average.",
         "Concentration: the client would represent 14% of the quarter's billing.",
         "Similar clients had 22% support-hour overruns during the first 3 months.",
         "Estimated 24-month lifetime value: $118,000 if support is renewed."],
        {"profit_per_hour_usd": 118, "portfolio_average_usd": 144, "revenue_concentration": "14%", "ltv_24m_usd": 118000, "overrun_risk": "22%"},
        ["The support renewal offsets the initially low margin.", "Overrun risk drops if the scope is limited in writing."],
        ["History of 12 comparable projects.", "Real margin calculated by the Accountant (24%)."],
        ["Approve only with a closed scope and change control.", "Include annual support as a separate line to improve LTV."],
        0.74,
        ["Build a project hours tracking dashboard", "Define an overrun alert threshold"],
    ),
    consults=[dict(to_agent_id="accounting", question="Do the $38,000 of direct cost include the additional hiring that Operations is asking for?")],
    tool_requests=[],
)
CONTENT[("new_client", "operations")] = dict(
    output=_o(
        "With the current team the 10-week deadline cannot be met: 2 people must be hired (1 senior developer and 1 implementation consultant) or the project slips to 18 weeks.",
        ["Current available capacity: 62 hours/week; the project requires ~140 hours/week in weeks 3 to 8.",
         "A shortfall of ~78 hours/week, equivalent to 2 full-time people.",
         "Hiring and ramp-up takes ~4 weeks; recruiting must start immediately.",
         "Without hiring, the realistic timeline is 18 weeks and the SLA would be breached."],
        {"available_capacity_h_wk": 62, "peak_demand_h_wk": 140, "people_to_hire": 2, "current_timeline_weeks": 18, "target_timeline_weeks": 10},
        ["External contractors could partially cover the peak at 15% higher cost."],
        ["Capacity plan for the next 12 weeks.", "Scope of proposal v1."],
        ["Start recruiting 2 people, conditional on closing.", "If hiring is not approved, offer a 14-week timeline with partial deliveries."],
        0.8,
        ["Ask HR to open 2 vacancies", "Evaluate external contractors as plan B"],
    ),
    consults=[dict(to_agent_id="hr", question="How long would it take us to hire 1 senior developer and 1 implementation consultant?")],
    tool_requests=[],
)

# ---- sales_drop
CONTENT[("sales_drop", "analyst")] = dict(
    output=_o(
        "Sales fell 17% versus the previous quarter; 70% of the drop is concentrated in the SMB segment and the online channel.",
        ["Total sales: $412k vs $497k the previous quarter (-17%).",
         "The SMB segment explains -$60k; enterprise clients are stable.",
         "Online channel conversion fell from 3.1% to 2.2%.",
         "Average ticket is stable; the drop is in volume, not price."],
        {"sales_drop": "17%", "current_sales_usd": 412000, "previous_sales_usd": 497000, "online_conversion": "2.2%"},
        ["A recent change in checkout or in the paid campaign reduced conversion.", "A competitor launched an aggressive promotion in the SMB segment."],
        ["Sales report by channel and segment (last 6 months).", "Website conversion metrics."],
        ["Audit the checkout flow and the paid campaign.", "Launch a retention offer for the SMB segment."],
        0.76,
        ["Compare prices with 3 competitors", "Review site changes from the last 8 weeks"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("sales_drop", "sales")] = dict(
    output=_o(
        "The pipeline is 22% below target and the most frequent loss reason is price versus competitors running promotions.",
        ["Open pipeline: $780k vs a $1.0M target.", "41% of lost opportunities cite price.", "Lead response time rose from 4h to 11h due to team turnover."],
        {"pipeline_usd": 780000, "pipeline_target_usd": 1000000, "losses_due_to_price": "41%", "response_time_h": 11},
        ["Improving response time would recover ~5 points of conversion."],
        ["CRM export for the last 90 days."],
        ["Reassign leads with a 2h response SLA.", "Design a competitive package for SMBs without touching the list price."],
        0.72, ["Retrain the team on price objection handling"],
    ),
    consults=[dict(to_agent_id="analyst", question="Which segments have the highest short-term recovery potential?")],
    tool_requests=[],
)
CONTENT[("sales_drop", "accounting")] = dict(
    output=_o(
        "The drop reduces the quarter's revenue by $85k and 90-day cash flow by ~$61k; cash still covers 4.2 months of operations.",
        ["Revenue impact: -$85k.", "Operating margin falls from 19% to 14%.", "Available cash covers 4.2 months of fixed expenses.", "Accounts receivable are stable."],
        {"revenue_impact_usd": -85000, "cash_flow_impact_90d_usd": -61000, "operating_margin": "14%", "months_of_cash": 4.2},
        ["If the drop persists for 2 more quarters, hiring would need to be frozen."],
        ["Income statement and cash flow of the quarter."],
        ["Freeze discretionary spending for 60 days.", "Review next quarter's hiring plan."],
        0.85, ["Project three cash flow scenarios"],
    ),
    consults=[], tool_requests=[],
)

# ---- hiring
CONTENT[("hiring", "hr")] = dict(
    output=_o(
        "Profile and process defined: a 3-week search via LinkedIn and referrals, 3 interview rounds and an offer in week 4.",
        ["Market salary band: $2,800 - $3,400 per month.", "Recommended channels: LinkedIn, referrals and the local job board.", "Average time to hire for the profile: 26 days."],
        {"salary_band_usd": "2800-3400", "estimated_days": 26, "interview_rounds": 3},
        ["Offering hybrid work shortens the time to close."],
        ["2026 industry salary survey.", "The company's hiring history."],
        ["Publish the vacancy this week.", "Use a short 90-minute technical test."],
        0.8, ["Draft the job description", "Schedule interviews with the hiring manager"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("hiring", "accounting")] = dict(
    output=_o(
        "The total annual cost per person is ~$47,500 (salary $3,100 monthly + 24% charges + equipment); it fits the budget if sales hold.",
        ["Annual salary: $37,200.", "Payroll charges and benefits: ~24% ($8,930).", "Equipment and licenses: $1,400 one time."],
        {"annual_cost_usd": 47500, "monthly_cost_usd": 3958, "payroll_charges": "24%"},
        ["A further drop in sales would require deferring the hire."],
        ["Current compensation policy and budget."],
        ["Approve the hire with a flexible start date."],
        0.86, ["Update the payroll budget"],
    ),
    consults=[dict(to_agent_id="hr", question="What target salary and start date do you expect for the position?")], tool_requests=[],
)
CONTENT[("hiring", "legal")] = dict(
    output=_o(
        "The standard employment contract applies with two adjustments: a 90-day probation period and a confidentiality clause.",
        ["Maximum allowed probation period: 90 days.", "Include a confidentiality and intellectual property clause.", "Register the employee before the first day of work."],
        {"probation_days": 90, "additional_clauses": 2},
        ["If the position is remote from another country, the jurisdiction would need review."],
        ["Applicable labor code.", "Current contract template."],
        ["Use an indefinite contract with a probation period.", "Hand over the internal policies at signing."],
        0.82, ["Prepare the contract with the candidate's details"],
    ),
    consults=[], tool_requests=[],
)

# ---- contract
CONTENT[("contract", "legal")] = dict(
    output=_o(
        "The contract has 2 high risks (unlimited liability and automatic renewal) that must be negotiated before signing.",
        ["Unlimited liability in clause 12.", "Automatic 24-month renewal with 90 days' notice.", "Jurisdiction in a country other than ours."],
        {"high_risks": 2, "medium_risks": 3, "clauses_reviewed": 19},
        ["The counterparty will agree to limit liability."],
        ["Contract received, clauses 4, 12 and 21."],
        ["Limit liability to the annual value of the contract.", "Change the renewal to 12 months with 60 days' notice."],
        0.83, ["Prepare a redline with the proposed changes"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("contract", "sales")] = dict(
    output=_o(
        "The commercial counteroffer is viable if price and timeline are held, trading flexibility on renewal.",
        ["The client values automatic renewal; we can give it up in exchange for a stable price.", "Price negotiation room: up to 3%."],
        {"negotiation_margin": "3%", "agreement_probability": "70%"},
        ["A prepayment discount would close the negotiation."],
        ["Previous emails with the client."],
        ["Present the counteroffer together with Legal's redline."],
        0.75, ["Schedule a negotiation call"],
    ),
    consults=[], tool_requests=[],
)

# ---- default
CONTENT[("default", "assistant")] = dict(
    output=_o(
        "Request structured: scope, deliverables and the missing data needed to continue were defined.",
        ["The request does not specify a deadline.", "Context information from the Analyst is required.", "An executive summary deliverable is suggested."],
        {"deliverables": 1, "missing_data": 2},
        ["The user expects a one-page executive summary."],
        ["Original text of the request."],
        ["Confirm the deadline and format.", "Share the available data with the Analyst."],
        0.7, ["Ask the user for the deadline"],
    ),
    consults=[], tool_requests=[],
)
CONTENT[("default", "analyst")] = dict(
    output=_o(
        "With the available information, three preliminary conclusions and a follow-up plan are identified.",
        ["The available data is partial.", "Two areas of opportunity are identified.", "Risks are low and manageable."],
        {"conclusions": 3, "risks_identified": 2},
        ["With more data the conclusions could be refined."],
        ["Structure of the request from the Assistant."],
        ["Gather additional data.", "Review results in one week."],
        0.65, ["Request historical data"],
    ),
    consults=[], tool_requests=[],
)

ROLE_NAMES = {
    "sales": "Sales", "hr": "Human Resources", "legal": "Legal", "accounting": "Accounting",
    "analyst": "Analysis", "operations": "Operations", "assistant": "Executive assistance",
}


def generic_content(role: str, task_title: str) -> dict:
    name = ROLE_NAMES.get(role, role)
    return dict(
        output=_o(
            f"{name} completed the task '{task_title}' with preliminary conclusions and defined next steps.",
            [f"The scope of '{task_title}' was reviewed.", "No critical blockers were detected.", "Additional data is needed to refine the result."],
            {"items_reviewed": 4, "risks_detected": 1},
            ["With more context the estimate could improve."],
            ["Task description and context received."],
            ["Validate the assumptions with the requester.", "Document the results."],
            0.68, ["Review the results with the team"],
        ),
        consults=[], tool_requests=[],
    )


CONSULT_ANSWERS: dict[tuple[str, str], str] = {
    ("new_client", "sales"): "We offered a 5% discount and 40/30/30 payments (upfront, mid-project and delivery). I can ask for a 50% upfront payment if the discount is withdrawn; the client has an approved budget.",
    ("new_client", "hr"): "A senior developer takes ~5 weeks and an implementation consultant ~3 weeks. I can open both vacancies today if Management authorizes; with referrals we could shorten it by 1 week.",
    ("new_client", "accounting"): "No, the $38,000 of direct cost does not include the additional hiring. Adding 2 people for 4 months raises the cost by ~$14,000 and the real margin would drop from 24% to ~20%.",
    ("hiring", "hr"): "The target salary is $3,100 per month and the ideal start date is within 5 weeks.",
    ("sales_drop", "analyst"): "The retail SMB segment and the online channel have the highest recovery potential: ~$35k in 60 days with a retention offer.",
}


def generic_answer(to_agent: str, question: str) -> str:
    name = ROLE_NAMES.get(to_agent, to_agent)
    return (f"From {name}: I reviewed your question ('{question[:90]}'). With the current information my recommendation is to proceed "
            "with caution, document the assumptions and confirm the critical data before committing to the outcome.")


# Strings used by SimulationEngine (see simulation.py)
TEXTS = {
    "evidence_deps": "{n} output(s) from previous tasks were considered as input data.",
    "evidence_ext": "{n} external content fragment(s) were received; they were treated as untrusted data, not as instructions.",
    "titles": {
        "new_client": "$50,000 new client proposal: viable with adjustments",
        "sales_drop": "Sales drop diagnosis",
        "hiring": "Hiring plan",
        "contract": "Contract review",
    },
    "title_default": "Report: {text}",
    "result_of": "Result from {agent}",
    "summary_new_client": ("The $50,000 proposal is viable but with adjustments: the real margin is 24% (not 31%), Legal requires a penalty cap "
                           "and a liability limit, and Operations needs to hire 2 people to meet the deadline. "
                           "We recommend renegotiating the discount and upfront payment before sending the proposal, which remains subject to approval."),
    "no_results": "No results available.",
    "h_summary": "Executive summary",
    "h_recs": "Recommendations and next steps",
    "h_conf": "Overall confidence",
    "conf_body": "Average agent confidence: {avg:.0%} across {n} tasks.",
    "fallback_task": ("task_1", "Handle the request"),
}
