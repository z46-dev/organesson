import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "bun:test";
import { ResourcePowerControl } from "./ResourcePowerControl";

const stoppedVM = {
    id: 17,
    name: "charlie-fedora",
    kind: "virtual_machine",
    power_state: "stopped",
    can_power_control: true
};

describe("resource power controls", () => {
    test("shows state-aware power actions only when authorized", () => {
        const authorizedMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: stoppedVM, onPower: () => undefined }));
        const runningMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, power_state: "running" }, onPower: () => undefined }));
        const readOnlyMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, can_power_control: false }, onPower: () => undefined }));

        expect(authorizedMarkup).toContain("data-action=\"start\">Start</button>");
        expect(authorizedMarkup).toContain(">stopped");
        expect(runningMarkup).toContain("data-action=\"stop\">Graceful shutdown</button>");
        expect(runningMarkup).toContain("data-action=\"restart\">Restart</button>");
        expect(readOnlyMarkup).toContain(">stopped");
        expect(readOnlyMarkup).not.toContain("data-action=");
    });

    test("hides controls for transitional or non-VM resources", () => {
        const startingMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, power_state: "starting" }, onPower: () => undefined }));
        const networkMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, kind: "network" }, onPower: () => undefined }));

        expect(startingMarkup).toContain(">starting");
        expect(startingMarkup).not.toContain("data-action=");
        expect(networkMarkup).toBe("");
    });
});
