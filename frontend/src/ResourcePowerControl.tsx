import { ChevronDown } from "lucide-react";
import type { Resource } from "./types";

type Props = {
    resource: Resource;
    onPower: (resource: Resource, action: "start" | "stop" | "restart") => void;
};

// Renders a VM state pill with actions allowed for its current power state.
export function ResourcePowerControl({ resource, onPower }: Props) {
    if (resource.kind !== "virtual_machine") {
        return null;
    }

    function runPowerAction(action: "start" | "stop" | "restart", event: React.MouseEvent<HTMLButtonElement>) {
        if (action === "restart" && !window.confirm(`Restart ${resource.name}?`)) {
            return;
        }
        event.currentTarget.closest("details")?.removeAttribute("open");
        onPower(resource, action);
    }

    const hasActions = resource.can_power_control && (resource.power_state === "running" || resource.power_state === "stopped");
    const stateClass = `power-state${resource.power_state === "running" ? " is-running" : ""}`;

    if (!hasActions) {
        return <span className={stateClass}>{resource.power_state}</span>;
    }

    return (
        <details className="vm-power-menu">
            <summary className={`${stateClass} has-actions`} aria-label={`Power state ${resource.power_state}; open power actions`}>
                {resource.power_state}<ChevronDown size={13} aria-hidden="true" />
            </summary>
            <div className="vm-power-actions">
                {resource.power_state === "stopped" ? <button type="button" data-action="start" onClick={(event) => runPowerAction("start", event)}>Start</button> : <>
                    <button type="button" data-action="stop" onClick={(event) => runPowerAction("stop", event)}>Graceful shutdown</button>
                    <button type="button" data-action="restart" onClick={(event) => runPowerAction("restart", event)}>Restart</button>
                </>}
            </div>
        </details>
    );
}
