import { ArrowUpRight, Boxes, Network, Server, Users } from "lucide-react";
import "./index.css";

type ResourceSummary = {
    label: string;
    value: string;
    detail: string;
    icon: typeof Server;
};

const resourceSummaries: ResourceSummary[] = [{
    label: "Virtual machines",
    value: "0",
    detail: "No virtual machines provisioned",
    icon: Server
}, {
    label: "Containers",
    value: "0",
    detail: "No containers provisioned",
    icon: Boxes
}, {
    label: "Networks",
    value: "0",
    detail: "No networks configured",
    icon: Network
}, {
    label: "Teams",
    value: "0",
    detail: "No teams created",
    icon: Users
}];

export function App() {
    return (
        <div className="app-shell">
            <header className="site-header">
                <a className="wordmark" href="/" aria-label="Organesson home">
                    <span className="wordmark-mark" aria-hidden="true">O</span>
                    <span>organesson</span>
                </a>
                <span className="environment-label">No environment selected</span>
            </header>

            <main className="main-content">
                <section className="page-introduction" aria-labelledby="page-title">
                    <p className="eyebrow">Overview</p>
                    <h1 id="page-title">Your infrastructure starts here.</h1>
                    <p className="lede">
                        Organesson will keep virtual infrastructure, networks, and access organized in one place.
                    </p>
                </section>

                <section className="summary-grid" aria-label="Resource summary">
                    {resourceSummaries.map(({ label, value, detail, icon: Icon }) => (
                        <article className="summary-card" key={label}>
                            <div className="summary-icon"><Icon size={18} strokeWidth={1.75} /></div>
                            <p className="summary-label">{label}</p>
                            <p className="summary-value">{value}</p>
                            <p className="summary-detail">{detail}</p>
                        </article>
                    ))}
                </section>

                <section className="getting-started" aria-labelledby="getting-started-title">
                    <div>
                        <p className="eyebrow">Getting started</p>
                        <h2 id="getting-started-title">Connect your first environment</h2>
                        <p>
                            Add a Proxmox VE environment to begin managing resources and assigning them to teams.
                        </p>
                    </div>
                    <button className="primary-action" type="button" disabled>
                        Add environment <ArrowUpRight size={16} aria-hidden="true" />
                    </button>
                </section>
            </main>
        </div>
    );
}

export default App;
