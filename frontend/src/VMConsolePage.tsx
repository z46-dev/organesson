import { useEffect, useState } from "react";
import type { ApiRequest } from "./api";
import type { Resource } from "./types";
import { VMConsolePanel } from "./VMConsolePanel";

type Props = { resourceID: number; request: ApiRequest; onError: (message: string) => void; detached?: boolean };

// Resolves an authorized VM before rendering its dedicated console page.
export function VMConsolePage({ resourceID, request, onError, detached = false }: Props) {
    const [resource, setResource] = useState<Resource | null>(null);
    const [loadError, setLoadError] = useState("");

    useEffect(() => {
        let active = true;
        request<{ resource: Resource }>(`/virtual-machines/${resourceID}`)
            .then((result) => active && setResource(result.resource))
            .catch((error: Error) => {
                if (active) {
                    setLoadError(error.message);
                    if (!detached) {
                        onError(error.message);
                    }
                }
            });
        return () => { active = false; };
    }, [resourceID, request, onError, detached]);

    useEffect(() => {
        if (resource) {
            document.title = `${resource.name} · Console`;
        }
    }, [resource]);

    if (loadError) {
        return <div className={`console-page${detached ? " is-detached" : ""}`}><div className="console-placeholder">Could not load this VM console.</div></div>;
    }

    if (!resource) {
        return <div className={`console-page${detached ? " is-detached" : ""}`}><div className="console-placeholder">Loading VM…</div></div>;
    }

    return <div className={`console-page${detached ? " is-detached" : ""}`}><VMConsolePanel resourceID={resourceID} resourceName={resource.name} allowed={resource.can_console_control} powerState={resource.power_state} detached={detached} /></div>;
}
