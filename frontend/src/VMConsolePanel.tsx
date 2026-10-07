import { useEffect, useRef, useState } from "react";
import { ArrowUpRight, Maximize2, Plug, Unplug } from "lucide-react";
import RFB from "@novnc/novnc";

type Props = { resourceID: number; resourceName: string; allowed: boolean; powerState: string; detached?: boolean };

// Opens an authenticated noVNC session on demand and provides compact console controls.
export function VMConsolePanel({ resourceID, resourceName, allowed, powerState, detached = false }: Props) {
    const target = useRef<HTMLDivElement>(null);
    const panel = useRef<HTMLElement>(null);
    const client = useRef<RFB | null>(null);
    const consoleCursor = useRef<string | null>(null);
    const canConnect = powerState === "running";
    const [connectionRequested, setConnectionRequested] = useState(detached && canConnect);
    const [connected, setConnected] = useState(false);
    const [canEmbedConsole, setCanEmbedConsole] = useState(detached);
    const [connectionError, setConnectionError] = useState("");
    const [state, setState] = useState(detached ? "Connecting…" : "Closed");

    useEffect(() => {
        if (!canConnect && connectionRequested) {
            setConnectionRequested(false);
        }
    }, [canConnect, connectionRequested]);

    useEffect(() => {
        if (detached) {
            setCanEmbedConsole(true);
            return;
        }
        const element = panel.current;
        if (!element) {
            return;
        }
        const updateAvailableHeight = () => {
            const hasRoom = (panel.current?.clientHeight ?? 0) >= 340;
            setCanEmbedConsole(hasRoom);
            if (!hasRoom) {
                setConnectionRequested(false);
            }
        }
        const observer = new ResizeObserver(updateAvailableHeight);
        observer.observe(element);
        updateAvailableHeight();
        return () => observer.disconnect();
    }, [detached]);

    useEffect(() => {
        if (!allowed || !canConnect || !connectionRequested || !target.current || (!detached && !canEmbedConsole)) {
            if (!connectionRequested) {
                setConnected(false);
                setState("Closed");
            }
            return;
        }

        let active = true;
        let screenResizeObserver: ResizeObserver | null = null;
        const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
        const consoleSocket = new WebSocket(`${scheme}//${window.location.host}/api/v1/virtual-machines/${resourceID}/console`);
        let consoleClient: RFB | null = null;
        setState("Connecting…");
        consoleSocket.onmessage = (event: MessageEvent) => {
            if (!active) {
                return;
            }
            if (typeof event.data !== "string" || event.data.length === 0) {
                setConnectionError("Could not read console credentials");
                consoleSocket.close();
                return;
            }
            const consolePassword = event.data;
            consoleSocket.onmessage = null;
            consoleClient = new RFB(target.current!, consoleSocket, { credentials: { password: consolePassword } });
            client.current = consoleClient;
            consoleClient.scaleViewport = true;
            if (target.current?.parentElement) {
                screenResizeObserver = new ResizeObserver(() => {
                    if (consoleClient) {
                        consoleClient.scaleViewport = true;
                    }
                });
                screenResizeObserver.observe(target.current.parentElement);
            }
            // Keep the guest's native resolution and scale its framebuffer to fit
            // the available console area without cropping or changing the VM.
            consoleClient.resizeSession = false;
            consoleClient.focusOnClick = true;
            consoleClient.addEventListener("connect", () => {
                if (active) {
                    setConnected(true);
                    setState("Connected");
                    consoleClient?.focus({ preventScroll: true });
                }
            });
            consoleClient.addEventListener("disconnect", () => {
                if (active) {
                    setConnected(false);
                    setState("Disconnected");
                }
            });
            consoleClient.addEventListener("securityfailure", () => active && setConnectionError("Console connection rejected"));
            consoleClient.addEventListener("credentialsrequired", () => consoleClient?.sendCredentials({ password: consolePassword }));
        };
        consoleSocket.onerror = () => active && setConnectionError("Console connection failed");
        consoleSocket.onclose = () => {
            if (active) {
                setConnected(false);
                setConnectionRequested(false);
                setState("Disconnected");
            }
        };

        return () => {
            active = false;
            screenResizeObserver?.disconnect();
            client.current = null;
            consoleClient?.disconnect();
            consoleSocket.close();
        };
    }, [allowed, canConnect, canEmbedConsole, connectionRequested, detached, resourceID]);

    function detach() {
        if (detached) {
            window.close();
            return;
        }
        const popup = window.open(`/console/${resourceID}?detached=1`, `organesson-console-${resourceID}`, "popup,width=1280,height=820,resizable=yes,scrollbars=no");
        popup?.focus();
    }

    function toggleFullscreen() {
        const screen = target.current?.parentElement;
        if (screen && !document.fullscreenElement) {
            void screen.requestFullscreen();
        } else if (document.fullscreenElement) {
            void document.exitFullscreen();
        }
    }

    function showLocalCursor() {
        const canvas = target.current?.querySelector("canvas");
        if (!canvas) {
            return;
        }
        consoleCursor.current = canvas.style.cursor;
        canvas.style.cursor = "default";
    }

    function restoreConsoleCursor() {
        const canvas = target.current?.querySelector("canvas");
        if (!canvas || consoleCursor.current === null) {
            return;
        }
        canvas.style.cursor = consoleCursor.current;
        consoleCursor.current = null;
    }

    return (
        <section className={`vm-console-panel${detached ? " is-detached" : ""}`} ref={panel} aria-label="Virtual machine console">
            <header className="console-toolbar">
                {!detached && !canEmbedConsole ? <div className="console-toolbar-actions">
                    <button className="quiet-button console-control" type="button" onClick={detach}><ArrowUpRight size={15} />Pop out</button>
                </div> : <>
                    <div className="console-title"><h4>{detached ? resourceName : "Console"}</h4>{detached && <span>VM {resourceID}</span>}</div>
                    <div className="console-toolbar-actions">
                        <span className="visually-hidden" role="status" aria-live="polite">{state}</span>
                        {allowed && (connectionRequested ? <button className="quiet-button console-control" type="button" onClick={() => setConnectionRequested(false)}><Unplug size={14} />Disconnect</button> : <button className="quiet-button console-control console-connect-control" type="button" onClick={() => { setConnectionError(""); setConnectionRequested(true); }} disabled={!canConnect} title={!canConnect ? "Start the VM before connecting to its console." : "Connect to the VM console"}><Plug size={14} />Connect</button>)}
                        {allowed && <button className="quiet-button console-control" type="button" onClick={() => client.current?.sendKey(0xffeb, "MetaLeft")} disabled={!connected} title="Send the Super (Windows/Meta) key">Super</button>}
                        {allowed && <button className="icon-button console-icon-control" type="button" onClick={() => client.current?.sendCtrlAltDel()} aria-label="Send Ctrl+Alt+Delete" title="Send Ctrl+Alt+Delete" disabled={!connected}><span className="console-key-hint">Ctrl<br />Alt<br />Del</span></button>}
                        {allowed && <button className="icon-button console-icon-control" type="button" onClick={toggleFullscreen} aria-label="Toggle console fullscreen" title="Fullscreen"><Maximize2 size={15} /></button>}
                        <button className="icon-button console-icon-control" type="button" onClick={detach} aria-label={detached ? "Detach and close window" : "Detach console"} title="Detach">{detached ? <Unplug size={15} /> : <ArrowUpRight size={16} />}</button>
                    </div>
                </>}
            </header>
            {!detached && !canEmbedConsole ? null : allowed ? <div className="console-screen-wrap" onMouseLeave={showLocalCursor} onMouseEnter={restoreConsoleCursor}>
                <div className="console-screen" ref={target} />
                {!connectionRequested && connectionError && <div className="console-error-overlay" role="alert">{connectionError}</div>}
            </div> : <div className="console-screen-wrap"><div className="console-placeholder">Your account does not have console access to this VM.</div></div>}
        </section>
    );
}
