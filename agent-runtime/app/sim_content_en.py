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
    "owner_task": ("responsable", "Handle the {area} part",
                   "Resolve the {area} part of the request with the available information and leave clear conclusions."),
}


# ------------------------------------------------------------------ chat (docs/architecture/chat-routing.md)
TASK_REASONS: dict[tuple[str, str], str] = {
    ("new_client", "ventas"): "knows the client and is the one who builds the commercial proposal",
    ("new_client", "legal"): "must review penalties, intellectual property and liability before anything goes out",
    ("new_client", "contador"): "has to confirm the real margin against the 31% budgeted",
    ("new_client", "analista"): "needs the Accountant's real margin to measure the client's profitability",
    ("new_client", "operaciones"): "knows whether current capacity covers the scope and the deadline",
    ("sales_drop", "analista"): "can read sales by product, channel and segment",
    ("sales_drop", "ventas"): "knows the pipeline and the reasons for lost deals",
    ("sales_drop", "contador"): "can measure how much the drop affects cash flow",
    ("hiring", "rrhh"): "runs recruiting and defines the profile",
    ("hiring", "contador"): "calculates the total cost of the hire",
    ("hiring", "legal"): "must review the employment contract and compliance",
    ("contract", "legal"): "is the one who reviews clauses and contract risks",
    ("contract", "ventas"): "has to align the commercial terms with Legal's findings",
}
ROLE_REASONS: dict[str, str] = {
    "sales": "owns the client relationship and commercial proposals",
    "hr": "handles hiring and labor rules",
    "legal": "reviews contracts, compliance and legal risks",
    "accounting": "controls margins, costs and invoicing",
    "analyst": "analyzes data and profitability with evidence",
    "operations": "knows operational capacity and delivery deadlines",
    "assistant": "coordinates and structures the request",
}

CHAT: dict = {
    "topic_labels": {
        "finance": "finance", "legal": "legal matters", "hr": "people and hiring", "sales": "sales",
        "data": "data and analysis", "operations": "operations", "general": "general topics",
    },
    "reasons": {
        "owner": "Owns {topic}",
        "coordinates": "Coordinates the team and handles general topics",
        "greeting": "Answers the greeting first",
        "peer_greeting": "Says a quick hello",
        "related": "Can add something from {topic}",
        "direct": "You wrote to them directly",
        "consult": "This is about {topic}; {name} can clarify it",
        "task": "Coordinates the request and splits the work",
    },
    "area": {
        "sales": "clients, proposals and pipeline", "hr": "hiring and labor matters",
        "legal": "contracts and legal risks", "accounting": "margins, costs and invoicing",
        "analyst": "data, metrics and profitability", "operations": "capacity, logistics and delivery",
        "assistant": "coordinating the team and organizing your requests",
    },
    "greet_primary": [
        "Hi! I'm {name}. How can I help you today?",
        "Hello! {name} here, ready for whatever you need. Tell me.",
        "Hey, how's it going? Tell me what you have in mind and we'll see who on the team takes it.",
        "Hi there! Good to see you. Shall we start on something, or just saying hello?",
        "Good morning. {name} here; if you need anything, let me know and I'll coordinate it.",
    ],
    "greet_peer": {
        "default": ["Hi, good morning.", "Hello! We're around.", "Hey, how are you?"],
        "accounting": ["Hi, good morning. Here with the numbers.", "Hello! Any question about costs or invoices, just ask.",
                       "Hey. The books are calm for now."],
        "legal": ["Hi. If there's a contract to review, I'm here.", "Hello! Keeping an eye out for any clause.",
                  "Hi, how are you. At your service."],
        "sales": ["Hi! Pipeline's up to date, tell us if there's something to close.", "Hey, how is everyone? Here with the clients.",
                  "Hello! Ready for anything commercial."],
        "hr": ["Hi, how's everything? Here with the team.", "Hello! Anything people-related, tell me.",
               "Hi, good morning everyone."],
        "analyst": ["Hi. Here with the data, tell me if you need an analysis.", "Hello! Numbers talk, I just listen.",
                    "Hi, how are you?"],
        "operations": ["Hi, good morning. Operations are on track.", "Hello! Here with the team's capacity.",
                       "Hi, at your service."],
    },
    "greet_self": [
        "Hi, I'm {name}, {title}. What do you need?",
        "Hello! {name} here. Tell me what you have in mind.",
        "Hey, tell me how I can help.",
        "Hi, how are you? I'm here for anything about {area}.",
    ],
    "howare": [
        "All good here, thanks for asking. And you, what are you up to?",
        "Doing well, nicely busy. Do you need anything from the team?",
        "Calm and on schedule. Tell me, what do you have in mind?",
    ],
    "thanks": [
        "You're welcome! I'm here for whatever you need.",
        "My pleasure. If anything else comes up, let me know.",
        "Anytime. Just write if you need something.",
        "Great, I'll be around.",
        "That's what we're here for. Anything else?",
    ],
    "ack": [
        "Noted. I'm here if anything comes up.",
        "Perfect. Whenever you want, we pick it up.",
        "Great, count on me for what's next!",
        "Got it. If you need anything, just say.",
        "Sounds good. I'll keep an eye out.",
    ],
    "help": [
        "I can help split the work: Sales, Legal, Accounting, HR, Analysis and Operations are available. Ask something from their area or give me a task and I'll coordinate it.",
        "I'm the team's assistant. Tell me what you need and I'll say who is the right person, or coordinate it myself. For quick questions, talk to each person directly.",
    ],
    "help_self": [
        "I take care of {area}. Ask me anything about it, or ask me to prepare something and I'll coordinate it.",
        "I can help you with {area}. What do you need?",
    ],
    "answer_general": [
        "I'm with you. To give you something useful, tell me a bit more: is it something to look up or do you want the team to prepare it?",
        "Got it. If you give me more context I'll tell you who on the team can help best.",
        "Noted. Shall we talk it through here, or should I turn it into a task for the team?",
    ],
    "answer_agenda": [
        "For your calendar I need the day, the time and who it's with. Do you want to review what's there or should I book something new?",
        "Sure, I'll help with that. Shall we look at today or at the week?",
        "I'll organize it. Tell me which meeting to move or create and I'll find the best slot.",
    ],
    "answer": {
        "accounting": [
            "On the finances: for now income and direct costs look reasonably aligned. If you like, I'll go through this month's balance and flag anything unusual. Which period are you interested in?",
            "Happy to. To give you a reliable number I need the period and whether we mean gross or net margin. Which one?",
            "Numbers are better with detail: I can put together a quick close with income, costs and cash flow. Tell me from when you want it.",
            "I can give you a figure, but I would rather the numbers be the right ones: do we mean billed or collected, and for which month?",
            "Before I weigh in, I reconcile income against costs for the period. Tell me the range and I will confirm with closed numbers.",
        ],
        "legal": [
            "From the legal side, the first thing is knowing which document or agreement is involved. Can you tell me more about the context?",
            "Good question. The prudent move is to review deadlines, penalties and liability before we commit. Do you have the draft?",
            "It depends on what was signed. If you share the contract, I'll tell you which risks I see.",
            "Without seeing the text I would rather not get ahead of myself. Can you send me the document, or at least the clauses that worry you?",
            "That has nuances: it changes with the scope and the jurisdiction. Tell me which agreement we mean and I will review it calmly.",
        ],
        "hr": [
            "On people: before deciding we should define the profile and the budget. Is it a new position or a replacement?",
            "Sure. To hire well I need the role, the salary band and when they need it.",
            "We can look at it. If you tell me the team size and current workload, I can give you a more concrete opinion.",
            "It is a delicate subject, so let us take it slowly. Are we talking about one person or about how the team is doing overall?",
            "Count on me. What worries you most: the workload, hiring times or the atmosphere?",
        ],
        "sales": [
            "In sales, follow-up is what moves the needle. Are we talking about a specific client or the pipeline in general?",
            "Here's how I see it: there are open opportunities, but it depends on each client. Which one do you want to discuss?",
            "Good point. If you give me the client and the approximate amount, I'll tell you what chance I see.",
            "Let us go for it! Give me the client name and what stage they are in, and we will plan how to close it.",
            "That is solved with follow-up and good timing. Which opportunity worries you most?",
        ],
        "analyst": [
            "With the available data I can look at trends, but I need to know which metric and which period. Which one?",
            "I could cross it with the history to see if it's a pattern or a one-off. Which period do we compare?",
            "Happy to look. Tell me the business question and I'll tell you what data we'd need.",
            "I am intrigued. If you tell me which metric matters, I will check whether there is a pattern behind it or just noise.",
            "Before drawing conclusions it helps to look at the distribution, not just the average. Which period shall we look at?",
        ],
        "operations": [
            "In operations it all comes down to capacity and deadlines. What volume or date do you have in mind?",
            "I'll look into it. To tell you whether we can make it I need the scope and the delivery date.",
            "Sure. Are we talking about team capacity, suppliers or logistics?",
            "Let us keep it practical: give me the date and volume and I will tell you whether we make it or what has to move.",
            "I will put it in real timings. Which delivery or process worries you?",
        ],
    },
    "contrib": {
        "legal": [
            "Just a legal note: before committing to anything, it's worth reviewing the payment and deadline clauses. I can do it if you like.",
            "Watch out on the contract side: if there are penalties or automatic renewal, let's look at them.",
        ],
        "accounting": [
            "Adding an accounting point: it's worth validating the cash flow impact before deciding.",
            "On the numbers side, I suggest confirming the real margin before moving on.",
        ],
        "hr": [
            "From the people side: if this means adding headcount, we'll assess it against the budget.",
            "An HR note: any team change goes through a review of the current workload.",
        ],
        "sales": [
            "From sales, it's worth telling the client early.",
            "On the commercial side, be careful with the deadlines promised to the client.",
        ],
        "analyst": [
            "If you like, I can back it up with historical data.",
            "I can look at the trend so we don't decide blind.",
        ],
        "operations": [
            "From operations, we'd need to confirm capacity is enough.",
            "An operations note: let's check the real timelines before promising dates.",
        ],
        "default": ["If needed, I'll chip in."],
    },
    "task_ack": [
        "Understood, let me see who on the team is the right fit and we'll set it up.",
        "Perfect. I'll coordinate it with the team and tell you who does what and why.",
        "On it. I'll check what this needs and split the work; I'll explain in a moment.",
    ],
    "task_ack_self": [
        "Sure, I'll take care of it. I'll set it up with the team and tell you who does what.",
        "On it. I'll see what I need from the others and let you know how it looks.",
        "Done, let me organize it with the team and I'll tell you the plan.",
    ],
    "redirect": [
        "{other} ({other_title}) handles that better. I'll ask and get back to you.",
        "That's more {other}'s area than mine. I'll ask and come back to you.",
        "For that, {other} ({other_title}) is the right person. Let me check with them.",
    ],
    "consult_q": [
        "{other}, the user is asking: \"{q}\". What should we tell them?",
        "{other}, can you help me with this? They ask: \"{q}\".",
    ],
    "consult_a": {
        "accounting": ["On the numbers, the prudent thing is to validate the period and margin with closed data before giving a figure."],
        "legal": ["Legally, the sound approach is to review the document and the deadlines before committing to anything."],
        "hr": ["From the people side, define the profile and budget first; then we look at hiring timelines."],
        "sales": ["Commercially, it depends on the client and the amount; with those I can give you a realistic probability."],
        "analyst": ["With the data we have I can give a trend, but I need the period to refine it."],
        "operations": ["In operations the key is capacity and delivery date; with those I can tell you if it's doable."],
        "default": ["I'll look into it and confirm."],
    },
}

# Regional tone applies to Spanish text only.
TONE_SUBS: dict[str, list[tuple[str, str]]] = {}

# ---- out-of-competence dialogue (see sim_content.py for the placeholders)
CHAT["deflect"] = {
    "accounting": [
        "That's not mine: {topic_area} is {other}'s. Talk to {other}; I can pass it on if you like.",
        "Outside my area. For {topic_area}, {other} is the right person. Shall I forward it?",
        "I stick to my own ({my_area}); {topic_area} belongs to {other} ({other_title}). I'll pass it on once you confirm.",
        "Not my remit. {other} handles {topic_area}. Tell me and I'll route it.",
        "My field is {my_area}. {other} takes care of {topic_area}. Should I hand it over?",
        "Better with {other}: {topic_area} isn't my field and I won't improvise.",
    ],
    "legal": [
        "I'd rather not weigh in on {topic_area}: {other} handles it and it should be seen by someone who knows. Shall I pass it on?",
        "With caution: {topic_area} isn't my area and I don't want to give you something imprecise. {other} is the one.",
        "That falls outside mine; {other} ({other_title}) should look at it. I can ask them if you like.",
        "It isn't for me to comment on {topic_area}. {other} can guide you better; shall I write to them?",
        "To avoid a misstep: {topic_area} is {other}'s. I'll stay with {my_area}.",
        "Before any misunderstanding: that's {other}'s, not mine. Shall I hand it over?",
    ],
    "sales": [
        "Oh, that's {other}'s turf, they handle {topic_area}! Shall I pass it on?",
        "Great idea, but not my thing! {other} is in charge of {topic_area}. Want me to connect you?",
        "{other} ({other_title}) does that better; {topic_area} is their ground. I'll pass it on in a moment!",
        "Happy to help with sales, but {topic_area} is {other}'s! Shall I connect you?",
        "Ah, I'm lost there: {other} masters {topic_area}. I'll pass it on, okay?",
        "Let's get you to the right person! {other} handles {topic_area}.",
    ],
    "hr": [
        "I understand what you need, but {other} handles {topic_area}; I want you to get it done right. Shall I pass it on?",
        "With all due care: that isn't my area. {other} ({other_title}) will help you better with {topic_area}.",
        "I'd love to help, but {topic_area} is {other}'s. Want me to let them know?",
        "So you're well taken care of: {other} deals with {topic_area}. I stay with {my_area}. Shall I hand over your request?",
        "Thanks for trusting me with this, though it isn't mine: {other} handles {topic_area}. I'll go with you through the handover if you like.",
        "Best if {other} looks at it; {topic_area} is their thing and you'll get a more useful answer.",
    ],
    "operations": [
        "Not mine. {topic_area}: {other}. Shall I pass it on?",
        "Straight: not my area. For {topic_area} go to {other} ({other_title}). I can forward it.",
        "I handle {my_area}; {topic_area} is {other}'s. Fastest is to pass it over. Okay?",
        "To save time: {other} solves that. Forward it now?",
        "Not mine. {other} handles {topic_area}; say so and I'll pass it on.",
        "That goes to {other}. I stay with {my_area}.",
    ],
    "analyst": [
        "Interesting, but {topic_area} isn't my ground; {other} will know more. Shall I pass it on?",
        "I'm curious, though it isn't mine: {other} handles {topic_area}. Want me to ask?",
        "I have no data or judgment there; {other} ({other_title}) does. Shall I ask them?",
        "Good question for {other}, who handles {topic_area}. I can add data afterwards if needed.",
        "Not my field, but intriguing; better {other}. Shall I pass the question along?",
        "That falls under {topic_area}, {other}'s. If you later want to cross it with data, I'm here.",
    ],
    "assistant": [
        "Happy to sort it out: {topic_area} is {other}'s. I'll pass it on right now, sound good?",
        "{other} ({other_title}) sees that better; I'll connect you right away.",
        "Let me put you through to {other}, who handles {topic_area}. Okay?",
        "To get it right, {other} is best. I coordinate and will get it to them.",
        "{other} is the right person for {topic_area}. Shall I pass your message?",
        "I'll coordinate it with {other}, who takes care of {topic_area}. Say the word and I'll start.",
    ],
}
CHAT["deflect_pair"] = {
    ("accounting", "sales"): ["Oh, that's {other}'s, they handle sales. I only handle the numbers; shall I pass it on?",
                              "Selling isn't mine. {other} is the one who sells; I'll connect you."],
    ("legal", "sales"): ["I review contracts, I don't sell. For the commercial side, {other}. Shall I pass it on?"],
    ("sales", "accounting"): ["The fine numbers are {other}'s! I sell, they count. Shall I pass it on?"],
    ("hr", "legal"): ["The contract is {other}'s; I look after people, I don't sign clauses. Shall I let them know?"],
}
CHAT["refuse_core"] = [
    "That work isn't mine: I'm reassigning it to {other} ({other_title}), who handles {topic_area}, and I'll tell you the plan.",
    "Not mine: I'll hand it to {other}, who takes care of {topic_area}, and coordinate it with the team.",
    "That's {topic_area}, so it's {other}'s. I'll reassign it and explain why.",
]
CHAT["limit_lead"] = {
    "accounting": ["Brief version.", "Plain fact.", "No detours."],
    "legal": ["With caution.", "Better to be clear.", "Prudence first."],
    "sales": ["Oh, I'm so sorry!", "Ugh, right now!", "I'm really sorry!"],
    "hr": ["I'm truly sorry.", "It pains me to say it.", "I know it's not what you hoped for."],
    "operations": ["Short and clear.", "No fluff.", "Straight."],
    "analyst": ["Too bad.", "Bad news.", "Heads up."],
    "assistant": ["Apologies.", "Sorry about that.", "I'm sorry."],
}
CHAT["limit_core"] = {
    "kill_switch": ["The team is paused by the emergency switch right now, so I can't do it.",
                    "An emergency brake is active; until it's lifted I can't work on this."],
    "paused": ["I've been paused, so I can't help with that for now.",
               "I'm paused until further notice; I can't take it on."],
    "budget": ["We hit the spending cap and I can't continue until it's raised.",
               "The budget is exhausted; without a higher cap I can't reply."],
    "read_only": ["We're in read-only mode: I can analyze, but not execute or send anything.",
                  "With read-only mode on, I can't make changes or send anything."],
    "no_connection": ["I have no connection to that tool, so I can't do it myself.",
                      "I'm missing the connection or permission for that; it would need to be enabled first."],
}
CHAT["handoff_yes"] = ["Perfect, I'll pass you to {other}.", "Okay, {other} takes it from here.", "Done, {other} will help you now."]
