import { useState } from "react";
import { TemplateCatalog } from "./TemplateCatalog";
import type { ApiRequest } from "./api";
import { ProxmoxResourcePolicy } from "./ProxmoxResourcePolicy";
import { AuthenticationSettings } from "./AuthenticationSettings";
import { APITokenSettings } from "./APITokenSettings";
import { UserDirectory } from "./UserDirectory";

type Props = {
    request: ApiRequest;
    onError: (message: string) => void;
    onNotice: (message: string) => void;
};

// Groups platform-wide configuration and credential management away from deployments.
export function AdminPage({ request, onError, onNotice }: Props) {
    const [section, setSection] = useState<"capacity" | "networks" | "templates" | "authentication" | "users" | "access">("capacity");

    return (
        <div className="admin-page">
            <div className="admin-settings-layout">
                <nav className="admin-settings-sidebar" aria-label="Administration settings">
                    <p className="eyebrow">Settings</p>
                    <button type="button" aria-current={section === "capacity" ? "page" : undefined} onClick={() => setSection("capacity")}>Quotas & placement</button>
                    <button type="button" aria-current={section === "networks" ? "page" : undefined} onClick={() => setSection("networks")}>Networks</button>
                    <button type="button" aria-current={section === "templates" ? "page" : undefined} onClick={() => setSection("templates")}>Source VMs</button>
                    <button type="button" aria-current={section === "authentication" ? "page" : undefined} onClick={() => setSection("authentication")}>Authentication</button>
                    <button type="button" aria-current={section === "users" ? "page" : undefined} onClick={() => setSection("users")}>Users</button>
                    <button type="button" aria-current={section === "access" ? "page" : undefined} onClick={() => setSection("access")}>API tokens</button>
                </nav>
                <div className="admin-settings-content">
                    {(section === "capacity" || section === "networks") && <ProxmoxResourcePolicy request={request} section={section} onError={onError} onNotice={onNotice} />}
                    {section === "templates" && <TemplateCatalog request={request} onError={onError} onNotice={onNotice} />}
                    {section === "authentication" && <AuthenticationSettings request={request} onError={onError} onNotice={onNotice} />}
                    {section === "users" && <UserDirectory request={request} onError={onError} onNotice={onNotice} />}
                    {section === "access" && <APITokenSettings request={request} onError={onError} onNotice={onNotice} />}
                </div>
            </div>
        </div>
    );
}
