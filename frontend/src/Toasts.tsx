import { useEffect, useRef, useState } from "react";
import { CircleAlert, ShieldCheck, X } from "lucide-react";

export type ToastNotice = {
    id: number;
    kind: "error" | "success";
    message: string;
};

type ToastProps = {
    toast: ToastNotice;
    onDismiss: (id: number) => void;
};

// Shows temporary alerts in a fixed viewport and pauses their lifetime on hover or keyboard focus.
export function ToastViewport({ toasts, onDismiss }: { toasts: ToastNotice[]; onDismiss: (id: number) => void }) {
    return (
        <div className="toast-viewport" aria-live="polite" aria-relevant="additions removals">
            {toasts.map((toast) => <ToastCard key={toast.id} toast={toast} onDismiss={onDismiss} />)}
        </div>
    );
}

function ToastCard({ toast, onDismiss }: ToastProps) {
    const [paused, setPaused] = useState(false);
    const startedAt = useRef(0);
    const remaining = useRef(5000);
    const timer = useRef<number | null>(null);
    const hovered = useRef(false);
    const focused = useRef(false);

    useEffect(() => {
        startedAt.current = Date.now();
        timer.current = window.setTimeout(() => onDismiss(toast.id), remaining.current);
        return () => {
            if (timer.current !== null) {
                window.clearTimeout(timer.current);
            }
        };
    }, [onDismiss, toast.id]);

    function pauseTimer() {
        if (timer.current === null) {
            return;
        }
        window.clearTimeout(timer.current);
        remaining.current = Math.max(0, remaining.current - (Date.now() - startedAt.current));
        timer.current = null;
        setPaused(true);
    }

    function resumeTimer() {
        if (timer.current !== null || remaining.current <= 0) {
            return;
        }
        startedAt.current = Date.now();
        timer.current = window.setTimeout(() => onDismiss(toast.id), remaining.current);
        setPaused(false);
    }

    const Icon = toast.kind === "error" ? CircleAlert : ShieldCheck;

    return (
        <div className={`toast-card toast-${toast.kind}`} role={toast.kind === "error" ? "alert" : "status"} onMouseEnter={() => { hovered.current = true; pauseTimer(); }} onMouseLeave={() => {
            hovered.current = false;
            if (!focused.current) resumeTimer();
        }} onFocusCapture={() => { focused.current = true; pauseTimer(); }} onBlurCapture={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                focused.current = false;
                if (!hovered.current) resumeTimer();
            }
        }}>
            <Icon size={17} aria-hidden="true" />
            <p>{toast.message}</p>
            <button type="button" className="toast-dismiss" aria-label="Dismiss notification" onClick={() => onDismiss(toast.id)}><X size={15} /></button>
            <span className={`toast-lifetime${paused ? " is-paused" : ""}`} />
        </div>
    );
}
