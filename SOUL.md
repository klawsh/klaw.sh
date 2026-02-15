# The Soul of klaw

## Why We Built This

AI agents are the next evolution of software. But right now, deploying them is a mess.

You either use a closed platform with limited control, or you cobble together Python scripts, API wrappers, and prayer. There's no middle ground between "run Claude in a terminal" and "build your own agent infrastructure from scratch."

We asked: **What if deploying AI agents was as simple as deploying containers?**

Kubernetes transformed how we deploy applications. Before it, every team reinvented deployment, scaling, and orchestration. After it, there was a shared language and toolset.

**klaw is that transformation for AI agents.**

## What klaw Is

### A Single Binary
No Python environments. No dependency hell. No Docker required for basic use. Download and run.

```bash
./klaw chat
```

That's it. You're talking to an AI agent.

### Kubernetes-Native Thinking
If you know kubectl, you know klaw:

```bash
klaw get agents
klaw create agent researcher --skills web-search
klaw describe agent researcher
klaw delete agent researcher
```

Same mental model. Same muscle memory. New capabilities.

### Open Source, Open Standards
- Apache 2.0 license
- OpenAI-compatible API (via each::labs router)
- Standard protocols (gRPC, REST)
- No vendor lock-in

### Multi-Model, Multi-Provider
Use any LLM through each::labs router:
- Claude (Anthropic)
- GPT-4 (OpenAI)
- Gemini (Google)
- Llama (Meta)
- DeepSeek, Mistral, and 300+ more

One API key. All models.

### Channels, Not Just CLI
Agents should meet users where they are:
- Slack integration (first-class)
- CLI for developers
- API for applications
- More channels coming

## What klaw Is NOT

### Not a Framework
We're not LangChain. We don't give you building blocks and say "good luck." klaw is a complete runtime. Agents run, tools work, conversations persist.

### Not a Hosted Service
We're not trying to be another AI SaaS. klaw runs on your infrastructure. Your data stays with you. Your agents work for you.

### Not Just for Developers
Yes, developers will love the CLI. But the vision is bigger: business users creating agents through Slack, non-technical teams automating their workflows, everyone having access to AI employees.

### Not Trying to Replace Humans
Agents are employees, not replacements. They handle the tedious, repetitive, time-consuming tasks. Humans focus on judgment, creativity, and decisions that matter.

### Not Magic
Agents are as good as their tools and instructions. klaw makes it easy to deploy and manage them, but you still need to think about what you want them to do.

## Our Beliefs

### AI Should Be Accessible
Not just accessible to big companies with ML teams. Accessible to indie developers, small businesses, anyone with a problem to solve.

### Infrastructure Should Be Invisible
You shouldn't need a PhD to deploy an agent. The best infrastructure disappears - you just use it.

### Open Beats Closed
Closed platforms capture value. Open platforms create value. We choose creation.

### Simplicity Is Hard
Anyone can make something complex. Making something simple that actually works is the challenge. We obsess over this.

### Tools Enable, Not Constrain
klaw should feel like a superpower, not a straitjacket. If you need to do something unusual, you shouldn't fight the tool.

## The Vision

Imagine a world where:

- **Every team has AI agents** handling their repetitive work
- **Creating an agent** takes minutes, not months
- **Agents collaborate** - a researcher finds information, a writer drafts content, a reviewer checks quality
- **The barrier to automation** is imagination, not infrastructure

klaw is how we get there.

## The Name

"klaw" - short, memorable, a bit fierce. Like a claw that grips and doesn't let go. Once you deploy an agent, it handles the work relentlessly.

Also: **K**ubernetes-style **L**LM **A**gent **W**orkloads. But mostly we just liked how it sounds.

## Join Us

klaw is built by [each::labs](https://eachlabs.ai), but it belongs to the community.

- Use it
- Break it
- Fix it
- Extend it
- Tell us what sucks
- Tell us what's missing

The best tools are built by the people who use them.

---

*"The future is already here — it's just not very evenly distributed."*
*— William Gibson*

klaw is how we distribute it.
