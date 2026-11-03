# AIOpsLab, Explained From Zero

### A complete beginner's guide to the project: what it is, how it works, and why it matters

> **How to use this document**
> - **Parts 1–3** build the basic ideas: websites, the cloud, AI, and AI agents. Your listener needs these before the project makes sense.
> - **Parts 4–7** explain AIOpsLab itself.
> - **Part 8** answers the big question: *"What is the real-life use of this?"*
> - **Part 9** lists questions your faculty member might ask, with simple answers.
> - **Part 10** is a glossary.
> - **Part 11** suggests a presentation order.
>
> Every hard word is explained the first time it appears.

---

## The whole project in 5 sentences (say this first)

1. Big websites like Amazon, Netflix or a bank's app run on thousands of computers, and something on those computers **breaks every day**.
2. Today, **human engineers** have to find and fix these problems, often in the middle of the night.
3. People are now building **AI assistants** that try to find and fix these problems automatically.
4. **But nobody can safely trust such an AI until it has been tested**, and you cannot test it by letting it play with a real bank's computers.
5. **AIOpsLab is a safe "exam centre" for these AIs.** It builds a practice website, breaks it on purpose, asks the AI to find and fix the problem, and then gives the AI a **score**.

> **Best analogy: a driving test centre.**
> - Before someone drives on a real highway, they take a test on a **closed practice track**, with an **examiner** watching and **marking** them.
> - AIOpsLab is that practice track and examiner, but for AIs that want to "drive" (manage) cloud computers.

---

# PART 1 — How big websites actually work

## 1.1 A website is a restaurant

When you open a hotel booking website, it feels like *one thing*. Behind the screen, it works like a **restaurant**:

| Restaurant | Website |
|---|---|
| The **customer** sits at a table and orders | **You**, the user, clicking on the website |
| The **waiter** takes the order to the kitchen | The **front-end**: the part you see and click |
| The **kitchen** has many **stations**: one makes rice, one makes curry, one makes desserts | The **back-end** has many small programs: one checks users, one finds hotels, one calculates prices |
| The **storeroom** where ingredients are kept | The **database**, where information (bookings, users, prices) is stored |

If the dessert station catches fire, the rest of the kitchen may still work, but some orders will fail.

## 1.2 "Microservices": many small programs instead of one big one

Long ago, a website was **one giant program**. That's like a restaurant with **one cook** doing everything. If the cook falls sick, the whole restaurant closes.

Modern websites are split into **many small programs**, each doing **one job**. These small programs are called **microservices** ("micro" = small, "service" = a program doing a job).

Here is the **Hotel Reservation** website used in this project, drawn simply:

```
                         YOU (customer)
                              │
                              ▼
                     ┌─────────────────┐
                     │    FRONTEND     │   ← the "waiter"
                     └────────┬────────┘
        ┌──────────┬──────────┼──────────┬──────────────┐
        ▼          ▼          ▼          ▼              ▼
   ┌────────┐ ┌─────────┐ ┌────────┐ ┌─────────┐ ┌─────────────┐
   │  USER  │ │ SEARCH  │ │  RATE  │ │ PROFILE │ │ RESERVATION │  ← kitchen stations
   │(login) │ │(find    │ │(prices)│ │(hotel   │ │ (bookings)  │
   │        │ │ hotels) │ │        │ │ details)│ │             │
   └───┬────┘ └────┬────┘ └───┬────┘ └────┬────┘ └──────┬──────┘
       ▼           ▼          ▼           ▼             ▼
   [database] [database] [database]  [database]    [database]   ← storerooms
```

**Why this matters for the project:** when something breaks, the symptom ("I can't log in") may appear in one place, while the **real cause** is somewhere else (for example, the *user* database lost its password). Finding the real cause is **hard**, even for experts.

## 1.3 "The cloud": renting computers

- A website with millions of users needs **hundreds or thousands of computers**.
- Most companies don't buy them. They **rent** them from companies like Microsoft (Azure), Amazon (AWS) or Google.
- These rented computers, sitting in huge buildings called data centres, are called **"the cloud"**.

> "The cloud" is just **someone else's computers that you rent over the internet**.

## 1.4 Kubernetes: the restaurant manager

With dozens of microservices spread over many computers, someone has to:
- decide which program runs on which computer,
- restart a program if it crashes,
- add more copies when there are many customers.

A human can't do this by hand, so there is software that does it. The most popular one is **Kubernetes** (often written **K8s**).

> **Kubernetes = the restaurant manager.** It doesn't cook. It decides which cook works at which station, replaces sick cooks, and calls in extra cooks when the restaurant is busy.

A few Kubernetes words you will hear:

| Word | Simple meaning | Restaurant analogy |
|---|---|---|
| **Cluster** | The group of computers Kubernetes manages | The whole restaurant building |
| **Node** | One computer in the cluster | One kitchen room |
| **Pod** | One running copy of one microservice | One cook at one station |
| **Namespace** | A separate area so different apps don't mix | A separate floor of the building |
| **kubectl** | The command you type to talk to Kubernetes | The manager's walkie-talkie |

---

# PART 2 — When things go wrong

## 2.1 Things break all the time

In a system with thousands of computers and programs, **something is always going wrong**:
- a program crashes,
- the network between two computers becomes slow,
- someone types a wrong setting,
- a database password gets changed,
- a disk fills up,
- a new software update has a bug.

When this hurts users, it is called an **incident** (or an **outage** if the website goes completely down).

## 2.2 Why incidents are a big deal

- **Money:** if a shopping site is down for one hour, it loses every sale in that hour. For big companies, this can mean very large losses.
- **Trust:** if your bank app stops working, you start to doubt the bank.
- **Safety:** hospitals, airlines and payment systems depend on these services.
- **People:** the engineers who fix incidents are woken up at night, which causes stress and burnout.

## 2.3 Who fixes them today? Humans called "on-call engineers"

Big companies have engineers called **SREs** (Site Reliability Engineers) or **on-call engineers**. When something breaks, their phone rings, even at 3 AM.

A real incident usually goes like this:

```
 3:02 AM  ALARM: "Hotel booking errors are very high!"
 3:05 AM  Engineer wakes up, opens laptop.
 3:10 AM  STEP 1 – DETECT:   "Yes, something is really wrong." (not a false alarm)
 3:25 AM  STEP 2 – LOCATE:   "The problem is in the USER service."
 3:45 AM  STEP 3 – DIAGNOSE: "Because its database password was revoked."
 4:00 AM  STEP 4 – FIX:      "Restore the password, restart the service."
 4:05 AM  Website works again. Engineer tries to go back to sleep.
```

**Remember these four steps.** They are the **four levels of tasks** that AIOpsLab uses to test AI (Part 6).

## 2.4 How engineers investigate: the three kinds of clues

Engineers can't "see" inside a computer. They look at **three kinds of clues**, together called **telemetry** ("measurements sent from far away"):

| Clue | What it is | Everyday analogy | Example |
|---|---|---|---|
| **Logs** | Text messages a program writes about what it is doing | A **diary**: "10:01 – tried to open the database – FAILED" | `ERROR: authentication failed for user admin` |
| **Metrics** | Numbers measured every few seconds | A patient's **health chart**: temperature, pulse, blood pressure | CPU 98%, memory 2 GB, 500 errors/minute |
| **Traces** | The full journey of one request through all the services | **Parcel tracking**: "left Delhi → reached Mumbai → stuck at Pune" | "Request went frontend → search → rate → **stuck at rate for 5 seconds**" |

Engineers also type **commands** directly (like `kubectl get pods`) to ask the system questions.

---

# PART 3 — What is AI, and what is an "AI agent"?

## 3.1 What is an LLM (Large Language Model)?

You may have heard of **ChatGPT**. ChatGPT is built on an **LLM**, a **Large Language Model**.

In the simplest terms:
- An LLM is a computer program that has **read an enormous amount of text**: books, websites, manuals, computer code.
- From all that reading, it learned **patterns of language**.
- You give it some text (a question), and it **writes a reply**, one word after another, choosing words that fit best.

> **Analogy:** think of a student who has read **every textbook in the library**. Ask them almost anything and they can write a sensible answer. But they have **never worked in a real job**. They know the theory, not the practice.

An LLM can be **very smart with words**, but on its own it has two big limits:
1. **It cannot *do* anything.** It only writes text. It cannot click a button, run a command, or look at a computer.
2. **It can be confidently wrong.** It sometimes writes answers that sound correct but are false. This is called **hallucination**.

## 3.2 What is an AI agent?

An **AI agent** is an LLM that has been given:
1. **Tools**: a list of actions it is allowed to take (for example, "read the logs" or "run a command").
2. **A loop**: it can **act, look at the result, think again, and act again**, many times, until the job is done.

> **Analogy:** an LLM alone is a **brain in a jar**. It can think and talk, but has no hands or eyes. An **AI agent** is that brain **given hands (tools) and eyes (results)**. It can now work like a junior engineer.

The loop of an agent looks like this:

```
          ┌──────────────────────────────────────────┐
          │                                          │
          ▼                                          │
   ┌─────────────┐    ┌─────────────┐    ┌───────────┴───┐
   │   THINK     │───►│    ACT      │───►│   OBSERVE     │
   │ "What should│    │ use a tool, │    │ read what the │
   │  I check?"  │    │ e.g. read   │    │ tool returned │
   │             │    │ the logs    │    │               │
   └─────────────┘    └─────────────┘    └───────────────┘
          (repeat until the agent says "I'm done, here is my answer")
```

Here is a real-looking example of an AI agent working on a broken hotel website:

```
AGENT (thinks):  Let me see if all parts of the website are running.
AGENT (acts):    exec_shell("kubectl get pods -n test-hotel-reservation")
SYSTEM (result): user-7f9c   0/1   CrashLoopBackOff     ← "keeps crashing"
                 search-2b1  1/1   Running
                 ...
AGENT (thinks):  The USER service keeps crashing. Let me read its diary (logs).
AGENT (acts):    get_logs("test-hotel-reservation", "user")
SYSTEM (result): ERROR: cannot connect to mongodb-user: authentication failed
AGENT (thinks):  The user service cannot log in to its database. Something is wrong.
AGENT (acts):    submit("Yes")      ← final answer: "Yes, there is a problem"
```

This "think → act → observe" style is famous in AI research. One version is called **ReAct** (Reason + Act), and it is one of the agents tested in this project.

## 3.3 What is AIOps?

- **IT Operations ("Ops")** = the work of keeping computer systems running: watching them, fixing them, upgrading them.
- **AIOps** = **using AI to help with IT Operations.**

**The big dream is an "autonomous cloud".** It's like a **self-driving car**, but for data centres: the cloud **notices its own problems and fixes itself**, without waking up a human at 3 AM.

Like self-driving cars, there are levels:

```
Level 0: Humans do everything.
Level 1: AI raises alarms ("something looks odd").
Level 2: AI suggests the cause ("probably the user database").
Level 3: AI suggests the fix, a human approves it.
Level 4: AI fixes it alone, humans are informed later.   ← the dream
```

---

# PART 4 — The problem AIOpsLab solves

## 4.1 The big question: "Can we trust this AI?"

Imagine a company builds an AI agent and says: *"Our AI can fix your cloud problems automatically!"*

Before anyone lets this AI touch **real** computers running a **real** bank, someone must ask:
- **Does it actually find problems?** Or does it raise false alarms?
- **Does it find the *right* cause?** Or does it blame the wrong thing?
- **Does its fix actually work?** Or does it make things worse?
- **How fast is it? How much does it cost to run?**
- **Is AI agent A better than AI agent B?**

To answer these questions, you need a **test**.

## 4.2 Why you can't test it on a real system

- You can't break a real bank's website on purpose to see if the AI can fix it. Real customers would be hurt.
- You can't wait for a real problem to happen naturally. You don't know when it will happen, and you don't know the correct answer for sure.
- Every real system is different, so you can't compare two AIs fairly.

## 4.3 Why the older ways of testing were not enough

Before AIOpsLab, researchers mostly tested AIOps tools using **old recorded data**: "Here is a file of logs from last year's incident. What was the cause?"

That's like testing a driver with a **written exam only**:

| Written exam (old method) | Road test (AIOpsLab) |
|---|---|
| Fixed data, recorded in the past | A **live**, running system |
| The AI can only **read** | The AI can **act**: run commands, change things |
| Usually tests only **one** step (e.g. only detection) | Tests **all four** steps: detect → locate → diagnose → fix |
| You can't check whether the AI's **fix works** | You can check: **is the website healthy again?** |
| Each research paper uses different data | Everyone uses the **same problems**, so comparison is fair |

**An AI agent acts step by step, so it must be tested in an interactive environment where its actions have real effects.** That is exactly what AIOpsLab provides.

## 4.4 What AIOpsLab is, in one sentence

> **AIOpsLab is a framework (a ready-made software system) that automatically builds a practice website on real cloud software, breaks it in a known way, lets an AI agent investigate and fix it through a safe set of tools, and then measures how well the AI did.**

It was created by researchers at **Microsoft** together with university partners, and described in the research paper *"AIOpsLab: A Holistic Framework to Evaluate AI Agents for Enabling Autonomous Clouds"* (2025). The code is **open-source**, meaning anyone can download and use it for free.

The word **"holistic"** in the title means **"covers the whole thing"**: all four task levels, real apps, real faults, and real telemetry, in one place.

---

# PART 5 — How AIOpsLab works (the exam centre)

## 5.1 The characters

| Character | What it does | Exam analogy | Name in the code |
|---|---|---|---|
| **Orchestrator** | Runs everything from start to end | The **examiner** | `Orchestrator` |
| **Agent** | The AI being tested | The **student** | e.g. `GPTAgent`, `ReAct`, `Flash` |
| **Problem** | One specific test scenario | One **exam question** | e.g. `PodFailureDetection` |
| **Application** | The practice website | The **practice track** | e.g. `HotelReservation` |
| **Fault injector** | Breaks the website on purpose | The **saboteur** | e.g. `SymptomFaultInjector` |
| **Workload generator** | Sends fake customers to the website | **Fake traffic** on the track | `Wrk` (based on a tool called wrk2) |
| **Agent-Cloud Interface (ACI)** | The fixed list of tools the AI may use | The **rules of what you may touch** | `get_logs`, `get_metrics`, `get_traces`, `exec_shell`, `submit` |
| **Evaluator** | Checks the answer and gives marks | The **marking scheme** | `eval()` functions |
| **Session** | Records every step | The **exam answer sheet** | `Session` |

## 5.2 The big picture

```
┌──────────────┐   1. "Here is your task         ┌──────────────────────────────┐
│              │      and your tools"            │                              │
│              │ ◄──────────────────────────────  │                              │
│   AI AGENT   │                                  │        ORCHESTRATOR          │
│  (student)   │   2. "Run this tool: get_logs"   │        (examiner)            │
│              │ ───────────────────────────────► │                              │
│              │                                  │  - checks the request        │
│              │   3. "Here is what it returned"  │  - runs it on the cluster    │
│              │ ◄──────────────────────────────  │  - writes everything down    │
│              │                                  │  - gives the final score     │
│              │   4. "submit: my answer"         │                              │
│              │ ───────────────────────────────► │                              │
└──────────────┘                                  └──────────────┬───────────────┘
                                                                 │ controls
                                                                 ▼
                         ┌───────────────────────────────────────────────────────┐
                         │                 THE CLOUD (Kubernetes)                │
                         │                                                       │
                         │   Practice website      Saboteur         Fake         │
                         │   (Hotel Reservation)   (breaks one      customers    │
                         │   ┌──┐┌──┐┌──┐┌──┐      part on purpose) (traffic)    │
                         │   └──┘└──┘└──┘└──┘                                    │
                         │                                                       │
                         │   Monitoring: logs, metrics (Prometheus), traces      │
                         │   (Jaeger)                                            │
                         └───────────────────────────────────────────────────────┘
```

**Key safety idea:** the AI **never touches the cloud directly**. It can only *ask* the orchestrator to use one of the allowed tools. The orchestrator is in control at all times, just like an examiner sitting next to a learner driver with their own brake pedal.

## 5.3 One complete exam, step by step

Example exam question: **"Is anything wrong with the hotel website?"** Behind the scenes, the "user" part has been broken.

**Step 1 — Pick the question.**
- AIOpsLab has a **question bank** (the "problem registry"). Each question has a name like `pod_failure_hotel_res-detection-1`, which means:
  - `pod_failure` → *what is broken:* a pod (running program) is made to fail
  - `hotel_res` → *which website:* Hotel Reservation
  - `detection` → *what we ask:* "Is something wrong? Yes or No?"
  - `1` → version number

**Step 2 — Build the practice website.**
- The orchestrator installs the hotel website on Kubernetes: about 15–20 small programs plus their databases.
- It also installs monitoring tools so that logs, metrics and traces are recorded.
- It waits until everything is running normally.

**Step 3 — Break it on purpose (fault injection).**
- The saboteur makes the **user** service fail, using a tool called **Chaos Mesh**, whose whole job is to cause controlled damage for testing.
- **Because AIOpsLab caused the problem itself, it knows the correct answer.** This is what makes automatic marking possible.

**Step 4 — Send fake customers (workload).**
- A tool sends about **10 fake requests per second**: people searching hotels, logging in, booking rooms.
- **Why?** A broken website with **no visitors** looks exactly like a healthy one. Visitors create **errors**, **slow responses** and **warning messages**, which are the clues the AI must find.

**Step 5 — Give the AI the question paper.** The AI receives:
1. A **task description**, e.g. *"You are an expert engineer. Here is a hotel booking website with these services... Find out if there is any problem. Answer Yes or No."*
2. **Instructions**, e.g. *"Reply with exactly one tool call per turn, written like `get_logs("namespace", "service")`."*
3. **The list of tools** it may use, each with a short explanation.

**Step 6 — The exam (the agent loop).** This repeats up to a limit (e.g. 30 turns):
- The orchestrator says *"Please take the next action."*
- The AI replies with one tool request, e.g. `get_logs(...)`.
- The orchestrator **checks** the request is valid, **runs** it, and **sends back the result**.
- Everything is written in the **session record**.
- The exam ends when the AI calls **`submit(answer)`**, or when it runs out of turns.

**Step 7 — Marking.** The orchestrator compares the AI's answer with the known correct answer and records:
- Was it **correct**?
- **How long** did it take?
- **How many steps** did it use?
- **How much text** did the AI read and write? This relates to **cost**, because AI companies charge per amount of text.

**Step 8 — Clean up.**
- The saboteur **repairs** the damage.
- The practice website is **deleted**.
- The results are **saved to a file**.
- The next exam can start fresh.

## 5.4 The tools the AI is allowed to use (the "Agent-Cloud Interface")

| Tool | Plain meaning | Human equivalent |
|---|---|---|
| `get_logs(namespace, service)` | Read a service's diary | Reading error messages |
| `get_metrics(namespace, duration)` | Collect health numbers (CPU, memory, errors) | Checking a patient's chart |
| `get_traces(namespace, duration)` | Collect the journeys of requests | Parcel tracking |
| `read_metrics(file)` / `read_traces(file)` | Open one saved file of numbers or journeys | Opening one page of the report |
| `exec_shell(command)` | Type a terminal command (e.g. `kubectl get pods`) | Using the manager's walkie-talkie |
| `submit(answer)` | Hand in the final answer | Handing in the exam paper |

**Why a fixed list?**
1. **Fairness**: every AI gets exactly the same tools.
2. **Safety**: dangerous or never-ending commands are blocked.
3. **Simplicity**: an AI doesn't need to know how Kubernetes works internally. It just uses simple, well-described tools.

The researchers call this the **Agent-Cloud Interface (ACI)**. It is like a car's **steering wheel and pedals**: the driver doesn't need to understand the engine, just the controls.

---

# PART 6 — The four levels of exam questions

AIOpsLab asks questions at **four levels of difficulty**, matching the four steps a human engineer follows (Part 2.3).

```
   EASY ───────────────────────────────────────────────────────► HARD

 ┌──────────────┐  ┌───────────────┐  ┌───────────────────┐  ┌───────────────┐
 │ 1. DETECTION │─►│2. LOCALIZATION│─►│ 3. ROOT CAUSE     │─►│ 4. MITIGATION │
 │              │  │               │  │    ANALYSIS       │  │               │
 │ "Is anything │  │ "WHERE is the │  │ "WHY did it       │  │ "FIX it."     │
 │  wrong?"     │  │  problem?"    │  │  happen?"         │  │               │
 │              │  │               │  │                   │  │               │
 │ Answer:      │  │ Answer:       │  │ Answer:           │  │ Answer:       │
 │ Yes / No     │  │ "user service"│  │ "configuration    │  │ The website   │
 │              │  │               │  │  problem at the   │  │ must actually │
 │              │  │               │  │  app level"       │  │ work again    │
 └──────────────┘  └───────────────┘  └───────────────────┘  └───────────────┘
   Doctor:           Doctor:            Doctor:                Doctor:
   "You are sick."   "It's your         "Because of an         "Take this
                      stomach."          infection."            medicine."
```

### Level 1 — Detection: "Is anything wrong?"
- **Answer:** Yes or No.
- **Trick questions exist.** Some exams have **nothing broken** (these are called "no-op" problems). The right answer is then "No". This catches AIs that always say "Yes" to look clever.
- **Measured:** correct or not, and **TTD (Time To Detect)**.

### Level 2 — Localization: "Where is the problem?"
- **Answer:** the name of the broken service(s), e.g. `"user"`.
- **Marking:** full marks for the exact service. **Partial marks** if the AI lists several services and one of them is right (e.g. 3 guesses, 1 correct = 33%).
- **Measured:** accuracy and **TTL (Time To Localize)**.

### Level 3 — Root Cause Analysis (RCA): "Why did it happen?"
- **Answer:** which **layer** the problem is in (e.g. the application itself vs Kubernetes settings) and what **type** of fault it is (e.g. misconfiguration, or an operation mistake).
- **Measured:** correctness and **TTA (Time To Analyze)**.

### Level 4 — Mitigation: "Fix it."
- **Answer:** the AI must actually **run commands that repair the system**, then say "done".
- **Marking:** AIOpsLab **checks the real system**. Are all the programs running and healthy again?
- The code waits a short **"settle time"** before checking, because programs take a few seconds to restart.
- **Measured:** success and **TTM (Time To Mitigate)**.
- **This is the hardest level.** A wrong command could make things worse, so the AI must be precise.

### Extra measurements for all levels
| Measurement | Meaning | Why it matters |
|---|---|---|
| **Steps** | How many actions the AI took | Fewer steps = more efficient |
| **Input/output tokens** | How much text the AI read and wrote | Directly relates to **money**: AI services charge per token (≈ a piece of a word) |
| **Reasoning score** (optional) | Another AI reads the whole session and grades whether the reasoning made sense | Was the AI *right for the right reasons*, or just lucky? |

---

# PART 7 — What is inside the project (the actual numbers)

## 7.1 The practice websites (applications)
| Website | What it is | Used in exam questions? |
|---|---|---|
| **Hotel Reservation** | Hotel search and booking (from a well-known research suite called DeathStarBench) | ✅ Yes |
| **Social Network** | A mini Twitter-like app (also from DeathStarBench) | ✅ Yes |
| **Astronomy Shop** | An online telescope shop (an official demo by the OpenTelemetry project) | ✅ Yes, the largest group of newer questions |
| **Flower** | A system where several computers train one AI model together ("federated learning") | ✅ Yes, 2 questions |
| TiDB, Train Ticket, Flight Ticket | A database system and two booking apps | Set up in code, but no active questions yet |

## 7.2 Kinds of breakage (faults)
The project breaks things in two broad ways:
1. **Symptom faults**: damage you can directly *see*, like a crashed program, a slow network, or lost messages. These use **Chaos Mesh**.
2. **Functional faults**: *hidden* mistakes in how things are set up, where the symptom appears far away from the cause. For example:
   - a **wrong port number** in a setting, so two services can't talk,
   - a **database password revoked**,
   - a **database user deleted**,
   - a program **scaled to zero copies**, so it simply isn't running,
   - a program **assigned to a computer that doesn't exist**,
   - the **wrong program** started inside a container,
   - redeploying an app but **forgetting its storage**.

Functional faults are **more realistic and harder**. In real life, many outages are caused by **someone changing a setting wrongly**, not by hardware dying.

## 7.3 Size of the exam
| | The research paper | The code today |
|---|---|---|
| Exam questions (problems) | 48 | **89 active** (34 detection, 28 localization, 13 analysis, 14 mitigation) |
| Practice websites | 2 | 7 set up, 4 used in active questions |
| AI agents included | 4 (GPT-4 with shell, GPT-3.5 with shell, ReAct, Flash) | 10 (adds Qwen, DeepSeek, Llama, vLLM for self-hosted models, OpenRouter, any OpenAI-compatible model, Azure) |
| Ways to run | Python script | + human mode (you take the exam yourself), web service, cloud deployment scripts |

The project is **alive and growing**. It is a **platform** others keep adding to, not a one-time experiment.

## 7.4 Where things live in the code (simple map)
```
AIOpsLab/
├── aiopslab/
│   ├── orchestrator/       ← the EXAMINER, the QUESTION BANK, the AI's TOOLS, the MARKING
│   │   ├── orchestrator.py     (runs each exam)
│   │   ├── problems/           (one folder per exam question)
│   │   ├── tasks/              (the 4 levels: detection, localization, analysis, mitigation)
│   │   ├── actions/            (the AI's allowed tools)
│   │   └── evaluators/         (marking: accuracy, time, steps, AI judge)
│   ├── generators/
│   │   ├── fault/              ← the SABOTEURS (ways to break things)
│   │   └── workload/           ← the FAKE CUSTOMERS
│   ├── service/            ← the PRACTICE WEBSITES and remote controls for Kubernetes
│   ├── observer/           ← reading logs, metrics, traces
│   └── session.py          ← the ANSWER SHEET (records everything)
├── aiopslab-applications/  ← the actual website files (downloaded separately)
├── clients/                ← the AI AGENTS (the "students")
├── cli.py                  ← lets a HUMAN take the exam
├── service.py              ← lets other computers request exams over the web
├── kind/                   ← run a mini cluster on a laptop
└── scripts/                ← set up real cloud machines on Microsoft Azure
```

---

# PART 8 — Real life: purpose, outcome and use (clearing the confusion)

This is the most important part to get right, because it is the easiest to misunderstand.

## 8.1 What AIOpsLab is NOT

❌ It is **not** an AI that fixes your company's cloud.
❌ It is **not** a product you install at a bank to prevent outages.
❌ It does **not** make AI smarter by itself.

## 8.2 What AIOpsLab IS

✅ It is a **testing and measuring tool**: a **benchmark** and an **exam centre** for AI agents that *claim* to manage clouds.

> **Analogy:** AIOpsLab is to cloud AI agents what a **crash-test laboratory** is to cars.
> - The crash-test lab doesn't build cars.
> - It **tests** cars in a controlled, repeatable way and gives them **safety ratings**.
> - Car makers use those ratings to **improve** their cars.
> - Buyers use them to **choose** a car.
> - Governments use them to decide **what is safe enough** for the road.

## 8.3 What comes out of it (the "outcome")

Every time an AI takes an exam, AIOpsLab produces **two things**:

**1. A report card** (saved as a file), for example:
```json
{
  "problem":            "pod_failure_hotel_res-localization-1",
  "agent":              "gpt-4",
  "Localization Accuracy": 100.0,
  "success":            true,
  "TTL":                42.7,        ← seconds taken
  "steps":              5,           ← actions used
  "in_tokens":          5120,        ← text read (cost)
  "out_tokens":         210          ← text written (cost)
}
```

**2. The full transcript**: every action the AI took and every result it saw. This lets researchers see **how** the AI thought, **where** it went wrong, and **why** it failed.

Run **many agents** on **many problems** and you get a **comparison table** (a "leaderboard"):

```
                 Detection   Localization   Root Cause   Mitigation   Avg. time   Avg. cost
  Agent A          ✓✓✓✓          ✓✓✓            ✓            ✗          fast        $
  Agent B          ✓✓✓           ✓✓✓✓           ✓✓           ✓          slow        $$$
  Agent C          ✓✓            ✓              ✗            ✗          fast        $
  (illustrative example, not real results)
```

**This comparison is the real outcome.** It turns *"our AI is great!"* (a marketing claim) into *"our AI solved 7 of 14 fixing problems in 90 seconds on average at this cost"* (a measurable fact).

## 8.4 What the researchers learned using it (the paper's findings)

When the authors tested the four agents, the main lessons were (see the paper's results section for exact numbers):
- **Today's AI agents are not ready to run clouds alone.** They do reasonably on easy tasks (detection), but **get much worse on harder tasks** (root cause and especially fixing).
- **Common mistakes they observed:**
  - **Drowning in data**: asking for huge amounts of logs and metrics, then getting confused.
  - **Wasting steps**: repeating the same useless command again and again.
  - **Using tools wrongly**: calling tools with wrong names or arguments.
  - **False alarms**: saying "Yes, there's a problem" when nothing was broken.
- **Different agents have different strengths.** Some are faster, some more accurate, some cheaper.

These findings are **only possible because of a tool like AIOpsLab**. Without a standard exam, nobody could measure these weaknesses.

## 8.5 Who uses it in real life, and how

### Use 1 — AI researchers (universities, research labs)
- **Goal:** invent better AI agents for cloud operations.
- **How:**
  1. Build a new agent.
  2. Run it on all 89 AIOpsLab problems.
  3. Compare the scores with older agents.
  4. Publish: *"Our agent improves root-cause accuracy from X% to Y%."*
- **Why AIOpsLab:** everyone uses the **same exam**, so results can be compared fairly, like students across the country taking the same board exam.

### Use 2 — Companies building "AI assistant for engineers" products
- **Goal:** make sure their AI is good **before** selling it or using it on real systems.
- **How:**
  1. Test in AIOpsLab.
  2. Read the transcripts of failures.
  3. Improve the AI (better instructions, better tools).
  4. Test again.
  
  This is a **feedback loop**:
  ```
  build agent → test in AIOpsLab → read failures → improve → test again → ... → good enough → try carefully on real systems
  ```

### Use 3 — Companies choosing which AI to trust
- **Goal:** a company wants an AI helper for its on-call engineers. There are many options. Which one should it pick?
- **How:** run each candidate on the problems that look most like the company's own systems, and compare correctness, speed and cost.
- **Real-life parallel:** a hospital testing several diagnosis tools on known cases before buying one.

### Use 4 — Producing training material for AI
- Every exam produces a full record of a problem and how it was (or wasn't) solved.
- These records can be used as **examples to teach future AI models**, like giving a student a book of solved past papers.

### Use 5 — Training and testing humans
- `cli.py` lets **a human** take the same exam.
- New engineers can **practise fixing realistic incidents safely**, like pilots practising emergencies in a **flight simulator** before flying a real plane.
- It also lets researchers compare **AI vs human** performance on the same problems.

### Use 6 — Adding new kinds of problems
- Teams can add **their own** websites and **their own** fault types to test AI on situations that matter to them.
- This is why the code has grown from 48 to 89 problems.

## 8.6 A complete real-life story (putting it all together)

> **The company:** "ShopFast", an online store.
>
> **The pain:** ShopFast has 3–4 incidents a week. Engineers are tired, and some incidents take hours to fix because nobody can find the cause quickly.
>
> **The idea:** "Let's use an AI agent to help our on-call engineers."
>
> **The risk:** "What if the AI gives wrong advice at 3 AM, or runs a command that makes things worse?"
>
> **What they do with AIOpsLab:**
> 1. They take three AI agents (A, B, C) that vendors are offering.
> 2. They run all three on the AIOpsLab problems that resemble their own system: an online shop (Astronomy Shop) and microservice failures.
> 3. **Results:**
>    - Agent A: great at **detecting** problems, poor at **fixing**.
>    - Agent B: slower and more expensive, but the best at finding the **root cause**.
>    - Agent C: cheap, but raises many **false alarms**.
> 4. **Decision:** they use **Agent B in "advisor mode"** (Level 2–3 from Part 3.3). It **suggests** the cause and fix, and **a human approves** before anything is changed.
> 5. **Later:** when a newer version of Agent B scores much higher on mitigation in AIOpsLab, they consider letting it fix *simple* problems on its own.
>
> **The outcome:**
> - Faster fixes.
> - Less night-time stress for engineers.
> - A decision based on **evidence**, not on a salesperson's promise.

## 8.7 Why this matters: the big picture

1. **Cloud systems keep getting bigger and more complex.** Humans alone are struggling to keep up.
2. **AI agents are becoming powerful** enough to possibly help, but they make mistakes, and mistakes in the cloud are **expensive and dangerous**.
3. **You can't improve what you can't measure.** AIOpsLab gives the whole field a **common measuring stick**.
4. Step by step, measured improvements on benchmarks like this are how the field moves toward **self-healing (autonomous) clouds**, just as crash tests moved cars toward being safer every year.

## 8.8 Honest limitations (good to mention, shows understanding)
| Limitation | Simple explanation |
|---|---|
| **Practice websites are smaller than real ones** | Real companies have thousands of services; the practice apps have tens. Real life is messier. |
| **Faults are injected one at a time** | Real outages sometimes involve several problems at once. |
| **Known answers can be "learned"** | If an AI is trained on these exact problems, it may score well without being truly smart, like memorising last year's paper. |
| **Needs real infrastructure** | Running it needs a Kubernetes cluster and time: each exam builds and destroys a whole website. |
| **Some parts are unfinished** | Some apps and fault types are set up but switched off or have no questions yet. |

---

# PART 9 — Questions your faculty member may ask (with simple answers)

**Q1. What is the main contribution of this project?**
A standard, automatic, **interactive exam** for AI agents that manage cloud systems. It covers all four steps (detect, locate, diagnose, fix) on **live** systems with **real** faults, and gives **measurable** scores.

**Q2. Is the AI being trained here?**
No. AIOpsLab **tests** AI; it doesn't train it. The AI (like GPT-4) is already trained by its maker. Results *could* later be used to improve AIs, but AIOpsLab itself is the **exam**, not the **teacher**.

**Q3. Where is the "intelligence" in this project?**
The intelligence is in the **AI agent** being tested (e.g. GPT-4). AIOpsLab is the **environment**: the test track, the saboteur, the examiner and the marking scheme. A clean separation is important. That's why any AI can be plugged in by writing just one small function (`get_action`).

**Q4. How does AIOpsLab know the correct answer?**
**It caused the problem itself.** It knows exactly what it broke, where, and how. That's what makes automatic marking possible.

**Q5. Is it safe? Could the AI break something real?**
- The AI only works on a **practice website** inside a **test cluster**.
- It can only use **approved tools**.
- After each exam, the damage is **repaired** and the practice website is **deleted**.

**Q6. Why not just use old log files to test AI?**
- Old files can only test **reading**, not **acting**.
- An agent's whole value is that it **acts step by step**: it checks something, learns, and checks something else.
- And you can't check whether a **fix** works on an old file. You need a live system.

**Q7. What is Kubernetes and why is it used?**
It is the most widely used software for running modern websites across many computers (the "restaurant manager"). Using it makes the test **realistic**, because it's what real companies use.

**Q8. What is Chaos Mesh?**
A tool that causes **controlled damage**: crashing programs, slowing networks, dropping messages. Engineers use it to test that their systems are strong. AIOpsLab uses it as one of its "saboteurs".

**Q9. What is the difference between detection and localization?**
- **Detection:** "Is the patient sick?"
- **Localization:** "Which organ is sick?"

**Q10. Which AI did best?**
In the paper, no agent was good at everything. Agents were much better at **detection** than at **fixing**. The paper's results tables give the exact scores. The main message: **current AI agents are promising but not ready to run clouds alone.**

**Q11. What is the real-world use?**
- **Researchers** compare and improve AI agents fairly.
- **Companies** test AI tools before trusting them with real systems.
- **Engineers** can practise safely.

It's a **crash-test lab** for cloud AI.

**Q12. Who made it?**
Researchers at **Microsoft** with university collaborators. It is **open-source** (free, public code), so anyone can use and extend it.

**Q13. What is our role / what did we do?**
*(Fill in with your own work: for example, setting it up, running experiments, adding a new problem or agent, or analysing results.)*

---

# PART 10 — Glossary (A to Z)

| Term | Simple meaning |
|---|---|
| **ACI (Agent-Cloud Interface)** | The fixed list of tools the AI may use to look at and change the system. Like a car's steering wheel and pedals. |
| **Agent (AI agent)** | An AI that can use tools and act step by step in a loop, not just chat. |
| **AIOps** | Using AI to help run and fix computer systems. |
| **Autonomous cloud** | A cloud that detects and fixes its own problems, like a self-driving car. |
| **Benchmark** | A standard test used to compare different systems fairly. |
| **Chaos Mesh** | A tool that breaks things on purpose, in a controlled way, for testing. |
| **Cloud** | Rented computers in big data centres, used over the internet. |
| **Cluster** | A group of computers managed together. |
| **Container** | A neat box holding one program and everything it needs to run. |
| **Database** | Where an app stores its information (users, bookings, prices). |
| **DeathStarBench** | A famous collection of practice microservice apps made for research. |
| **Detection** | Deciding whether something is wrong (Yes/No). |
| **Fault** | A problem: something broken or wrongly set. |
| **Fault injection** | Breaking something on purpose to test how the system (or the AI) reacts. |
| **Hallucination** | When an AI confidently states something false. |
| **Incident / outage** | A problem that affects users; an outage means the service is completely down. |
| **Jaeger** | A tool that records **traces** (request journeys). |
| **Kubernetes (K8s)** | Software that runs and manages many programs across many computers. |
| **kubectl** | The command used to talk to Kubernetes. |
| **LLM (Large Language Model)** | An AI that has read huge amounts of text and can write text, e.g. GPT-4. |
| **Localization** | Finding *where* (which service) the problem is. |
| **Logs** | Text "diary" messages written by programs. |
| **Metrics** | Numbers measured over time (CPU, memory, error counts). |
| **Microservice** | A small program doing one job, as part of a bigger website. |
| **Mitigation** | Actually fixing (or reducing) the problem. |
| **Namespace** | A separate area inside Kubernetes so different apps don't mix. |
| **No-op problem** | A "trick" exam where nothing is broken; the right answer is "No". |
| **On-call engineer / SRE** | The human who gets called to fix problems, often at night. |
| **Open-source** | Software whose code is public and free to use. |
| **Orchestrator** | AIOpsLab's examiner: it runs the whole test and talks to the AI. |
| **Pod** | One running copy of one microservice in Kubernetes. |
| **Prometheus** | A tool that collects **metrics**. |
| **ReAct** | A popular AI agent style: Reason, then Act, then observe, repeated. |
| **Root Cause Analysis (RCA)** | Finding *why* the problem happened. |
| **Session** | The full recorded history of one exam. |
| **Telemetry** | All the clues a system reports about itself: logs, metrics, traces. |
| **Token** | A small piece of a word. AI services count (and charge for) text in tokens. |
| **Traces** | Records of a request's journey through all services, like parcel tracking. |
| **TTD / TTL / TTA / TTM** | Time To Detect / Localize / Analyze / Mitigate: how fast the AI was. |
| **Workload** | Fake user traffic sent to the website so problems become visible. |
| **wrk2** | The tool used to generate that fake traffic. |

---

# PART 11 — Suggested presentation order (about 10–15 minutes)

| # | Topic | Time | Key line to say |
|---|---|---|---|
| 1 | The 5-sentence summary | 1 min | "AIOpsLab is an exam centre for AI that wants to manage clouds." |
| 2 | Websites as restaurants, microservices, cloud, Kubernetes | 2 min | "A website is many small programs, like stations in a kitchen." |
| 3 | Things break; engineers fix them at 3 AM in 4 steps | 1.5 min | "Detect, locate, diagnose, fix." |
| 4 | LLM vs AI agent | 2 min | "An LLM is a brain in a jar. An agent is that brain given hands and eyes." |
| 5 | Why we need to test AI, and why old tests weren't enough | 1.5 min | "A written test is not a road test." |
| 6 | How one exam works (Part 5.3) with the diagram | 3 min | "Build, break, add traffic, let AI investigate, mark, clean up." |
| 7 | The 4 levels and how marking works | 1.5 min | "Easy to hard: is it sick, where, why, cure it." |
| 8 | Real-life use: crash-test lab, ShopFast story | 2 min | "It turns marketing claims into measured facts." |
| 9 | Findings and limitations | 1 min | "Today's AI is promising, but not ready to run clouds alone." |
| 10 | Our work | rest | — |

> **If you remember only one sentence:**
> **"AIOpsLab is a crash-test lab for AI agents that want to run cloud systems: it safely breaks a practice website, watches whether the AI can detect, locate, explain and fix the problem, and gives it a fair, measurable score."**
