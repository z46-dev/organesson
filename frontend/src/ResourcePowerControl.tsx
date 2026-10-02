import type { Resource } from "./types";

type Props = {
    resource: Resource;
    onPower: (resource: Resource, action: "start" | "stop" | "restart") => void;
};

// Renders power controls only when the API confirms permission and a known VM state.
export function ResourcePowerControl({ resource, onPower }: Props) {
    if (resource.kind !== "virtual_machine" || !resource.can_power_control || (resource.power_state !== "running" && resource.power_state !== "stopped")) {
        return null;
    }

    function runPowerAction(action: "start" | "stop" | "restart") {
        if (action === "restart" && !window.confirm(`Restart ${resource.name}?`)) {
            return;
        }
        onPower(resource, action);
    }

    return (
        <div className="resource-controls">
            <div><strong>Power</strong></div>
            {resource.power_state === "stopped" ? <button className="primary-action" type="button" data-action="start" onClick={() => runPowerAction("start")}>Start</button> : <>
                <button className="secondary-action" type="button" data-action="stop" onClick={() => runPowerAction("stop")}>Graceful shutdown</button>
                <button className="secondary-action" type="button" data-action="restart" onClick={() => runPowerAction("restart")}>Restart</button>
            </>}
        </div>
    );
}
