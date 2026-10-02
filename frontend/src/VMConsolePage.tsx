import { useEffect, useRef, useState } from "react";
import { ArrowUpRight, Maximize2, Power } from "lucide-react";
import RFB from "@novnc/novnc";
import type { ApiRequest } from "./api";
import type { Resource } from "./types";

type Props = { resourceID: number; request: ApiRequest; onError: (message: string) => void };

// Displays the noVNC client for one authorized VM and supports a dedicated pop-out window.
export function VMConsolePage({ resourceID, request, onError }: Props) {
    const target = useRef<HTMLDivElement>(null);
    const [resource, setResource] = useState<Resource | null>(null);
    const [state, setState] = useState("Connecting to VM…");

    useEffect(() => {
        let active = true;
        request<{ resource: Resource }>(`/virtual-machines/${resourceID}`)
            .then((result) => active && setResource(result.resource))
            .catch((error: Error) => active && onError(error.message));
        return () => { active = false; };
    }, [resourceID, request, onError]);

    useEffect(() => {
        if (!target.current || !resource?.can_console_control) {
            return;
        }
        let active = true;
        const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
        const consoleSocket = new WebSocket(`${scheme}//${window.location.host}/api/v1/virtual-machines/${resourceID}/console`);
        let consoleClient: RFB | null = null;
        setState("Connecting to VM…");
        consoleSocket.onmessage = (event: MessageEvent) => {
            if (!active) {
                return;
            }
            if (typeof event.data !== "string" || event.data.length === 0) {
                setState("Could not read console credentials");
                consoleSocket.close();
                return;
            }
            const consolePassword = event.data;
            consoleSocket.onmessage = null;
            consoleClient = new RFB(target.current!, consoleSocket, { credentials: { password: consolePassword } });
            setState("Authenticating to VM…");
            consoleClient.scaleViewport = true;
            consoleClient.resizeSession = true;
            consoleClient.focusOnClick = true;
            consoleClient.addEventListener("connect", () => setState("Connected"));
            consoleClient.addEventListener("disconnect", () => setState("Disconnected"));
            consoleClient.addEventListener("securityfailure", () => setState("Console connection rejected"));
            consoleClient.addEventListener("credentialsrequired", () => consoleClient?.sendCredentials({ password: consolePassword }));
        };
        consoleSocket.onerror = () => active && setState("Console connection failed");
        consoleSocket.onclose = () => active && setState("Disconnected");
        return () => {
            active = false;
            consoleClient?.disconnect();
            consoleSocket.close();
        };
    }, [resourceID, resource?.can_console_control]);

    function popOut() {
        window.open(window.location.href, `organesson-console-${resourceID}`, "popup,width=1280,height=800,resizable=yes,scrollbars=no");
    }

    return (
        <section className="console-page" aria-label="Virtual machine console">
            <header className="console-toolbar">
                <div><p className="eyebrow">Virtual machine · {resource?.id ?? resourceID}</p><h1>{resource?.name ?? "Console"}</h1></div>
                <div className="console-toolbar-actions"><span className="console-status"><i />{state}</span><button className="icon-button" type="button" onClick={popOut} aria-label="Pop out console" title="Pop out"><ArrowUpRight size={16} /></button><a className="quiet-button" href="/"><Power size={15} />Close</a></div>
            </header>
            {!resource ? <div className="console-placeholder">Loading VM…</div> : !resource.can_console_control ? <div className="console-placeholder">Your account does not have console access to this VM.</div> : <div className="console-screen-wrap"><div className="console-screen" ref={target} /><span className="console-hint"><Maximize2 size={13} /> Click the console to send keyboard input</span></div>}
        </section>
    );
}
