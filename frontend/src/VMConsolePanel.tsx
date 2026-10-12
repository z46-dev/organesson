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
    const escapeHoldTimer = useRef<number | null>(null);
    const escapeHeld = useRef(false);
    const escapeReleasedConsole = useRef(false);
    const keyboardCapture = useRef(false);
    const superKeyHeldRef = useRef(false);
    const canConnect = powerState === "running";
    const [connectionRequested, setConnectionRequested] = useState(detached && canConnect);
    const [connected, setConnected] = useState(false);
    const [canEmbedConsole, setCanEmbedConsole] = useState(detached);
    const [connectionError, setConnectionError] = useState("");
    const [keyboardCaptureError, setKeyboardCaptureError] = useState("");
    const [superKeyHeld, setSuperKeyHeld] = useState(false);
    const [state, setState] = useState(detached ? "Connecting…" : "Closed");

    function releaseSuperKey() {
        if (!superKeyHeldRef.current) {
            return;
        }
        client.current?.sendKey(0xffeb, "MetaLeft", false);
        superKeyHeldRef.current = false;
        setSuperKeyHeld(false);
    }

    function toggleSuperKey() {
        if (!client.current || !connected) {
            return;
        }
        if (superKeyHeldRef.current) {
            releaseSuperKey();
            return;
        }
        client.current.sendKey(0xffeb, "MetaLeft", true);
        superKeyHeldRef.current = true;
        setSuperKeyHeld(true);
        client.current.focus({ preventScroll: true });
    }

    useEffect(() => {
        function releaseKeyboardCapture(exitFullscreen: boolean) {
            const keyboard = (navigator as Navigator & { keyboard?: { unlock: () => void } }).keyboard;
            keyboard?.unlock();
            keyboardCapture.current = false;
            if (escapeHoldTimer.current !== null) {
                window.clearTimeout(escapeHoldTimer.current);
                escapeHoldTimer.current = null;
            }
            if (exitFullscreen && document.fullscreenElement) {
                void document.exitFullscreen();
            }
        }

        function handleKeyDown(event: KeyboardEvent) {
            if (!keyboardCapture.current || event.code !== "Escape") {
                return;
            }
            event.preventDefault();
            event.stopImmediatePropagation();
            if (event.repeat || escapeHeld.current) {
                return;
            }
            escapeHeld.current = true;
            escapeReleasedConsole.current = false;
            escapeHoldTimer.current = window.setTimeout(() => {
                escapeReleasedConsole.current = true;
                releaseKeyboardCapture(true);
            }, 1200);
        }

        function handleKeyUp(event: KeyboardEvent) {
            if (event.code !== "Escape" || !escapeHeld.current) {
                return;
            }
            event.preventDefault();
            event.stopImmediatePropagation();
            escapeHeld.current = false;
            if (escapeHoldTimer.current !== null) {
                window.clearTimeout(escapeHoldTimer.current);
                escapeHoldTimer.current = null;
            }
            if (!escapeReleasedConsole.current) {
                client.current?.sendKey(0xff1b, "Escape");
            }
            escapeReleasedConsole.current = false;
        }

        function handleFullscreenChange() {
            if (!document.fullscreenElement) {
                releaseKeyboardCapture(false);
            }
        }

        function handleWindowBlur() {
            releaseSuperKey();
        }

        document.addEventListener("keydown", handleKeyDown, true);
        document.addEventListener("keyup", handleKeyUp, true);
        document.addEventListener("fullscreenchange", handleFullscreenChange);
        window.addEventListener("blur", handleWindowBlur);
        return () => {
            document.removeEventListener("keydown", handleKeyDown, true);
            document.removeEventListener("keyup", handleKeyUp, true);
            document.removeEventListener("fullscreenchange", handleFullscreenChange);
            window.removeEventListener("blur", handleWindowBlur);
            releaseSuperKey();
            releaseKeyboardCapture(false);
        };
    }, []);

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
                    releaseSuperKey();
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

    async function toggleFullscreen() {
        const screen = target.current?.parentElement;
        if (screen && !document.fullscreenElement) {
            setKeyboardCaptureError("");
            try {
                await screen.requestFullscreen();
                const keyboard = (navigator as Navigator & { keyboard?: { lock: () => Promise<void> } }).keyboard;
                if (!keyboard) {
                    setKeyboardCaptureError("Keyboard capture is not supported by this browser.");
                    return;
                }
                await keyboard.lock();
                keyboardCapture.current = true;
                client.current?.focus({ preventScroll: true });
            } catch {
                setKeyboardCaptureError("Keyboard capture was blocked; browser shortcuts may still apply.");
            }
        } else if (document.fullscreenElement) {
            const keyboard = (navigator as Navigator & { keyboard?: { unlock: () => void } }).keyboard;
            keyboard?.unlock();
            keyboardCapture.current = false;
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
                    <div className="console-toolbar-start">
                        <div className="console-title"><h4>{detached ? resourceName : "Console"}</h4>{detached && <span>VM {resourceID}</span>}</div>
                        {allowed && <div className="console-special-keys" aria-label="Special keys">
                            <button className={`icon-button console-icon-control console-special-key${superKeyHeld ? " is-pressed" : ""}`} type="button" onClick={toggleSuperKey} disabled={!connected} aria-pressed={superKeyHeld} title={superKeyHeld ? "Release the held Super key" : "Hold Super, focus the console, then press another key; click again to release"}>{superKeyHeld ? "Super held" : "Super"}</button>
                            <button className="icon-button console-icon-control console-special-key" type="button" onClick={() => client.current?.sendCtrlAltDel()} aria-label="Send Ctrl+Alt+Delete" title="Send Ctrl+Alt+Delete" disabled={!connected}>Ctrl+Alt+Del</button>
                        </div>}
                        {keyboardCaptureError && <span className="console-keyboard-message" role="status">{keyboardCaptureError}</span>}
                    </div>
                    <div className="console-toolbar-actions">
                        <span className="visually-hidden" role="status" aria-live="polite">{state}</span>
                        {allowed && !connectionRequested && <button className="quiet-button console-control console-connect-control" type="button" onClick={() => { setConnectionError(""); setConnectionRequested(true); }} disabled={!canConnect} title={!canConnect ? "Start the VM before connecting to its console." : "Connect to the VM console"}><Plug size={14} />Connect</button>}
                        {allowed && connectionRequested && !detached && <button className="icon-button console-icon-control" type="button" onClick={() => setConnectionRequested(false)} aria-label="Disconnect" title="Disconnect"><Unplug size={15} /></button>}
                        {detached && <button className="icon-button console-icon-control" type="button" onClick={detach} aria-label="Close console window" title="Close console window"><Unplug size={15} /></button>}
                        {allowed && <button className="icon-button console-icon-control" type="button" onClick={toggleFullscreen} aria-label="Toggle console fullscreen" title="Fullscreen · hold Esc to release keyboard capture"><Maximize2 size={15} /></button>}
                        {!detached && <button className="icon-button console-icon-control" type="button" onClick={detach} aria-label="Detach console" title="Detach"><ArrowUpRight size={16} /></button>}
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
