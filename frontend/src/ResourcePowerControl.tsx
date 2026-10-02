import type { Resource } from "./types";

type Props = {
    resource: Resource;
    onPower: (resource: Resource) => void;
};

// Renders power controls only when the API confirms permission and a known VM state.
export function ResourcePowerControl({ resource, onPower }: Props) {
    if (resource.kind !== "virtual_machine" || !resource.can_power_control || (resource.power_state !== "running" && resource.power_state !== "stopped")) {
        return null;
    }

    return (
        <div className="resource-controls">
            <div><strong>Power</strong></div>
            <button className="primary-action" type="button" onClick={() => onPower(resource)}>
                {resource.power_state === "running" ? "Stop" : "Start"}
            </button>
        </div>
    );
}
