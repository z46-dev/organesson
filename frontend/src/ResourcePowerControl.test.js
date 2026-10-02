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
    test("offers start and stop only when the API grants power control", () => {
        const authorizedMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: stoppedVM, onPower: () => undefined }));
        const runningMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, power_state: "running" }, onPower: () => undefined }));
        const readOnlyMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, can_power_control: false }, onPower: () => undefined }));

        expect(authorizedMarkup).toContain(">Start</button>");
        expect(runningMarkup).toContain(">Stop</button>");
        expect(readOnlyMarkup).toBe("");
    });

    test("hides controls for transitional or non-VM resources", () => {
        const startingMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, power_state: "starting" }, onPower: () => undefined }));
        const networkMarkup = renderToStaticMarkup(createElement(ResourcePowerControl, { resource: { ...stoppedVM, kind: "network" }, onPower: () => undefined }));

        expect(startingMarkup).toBe("");
        expect(networkMarkup).toBe("");
    });
});
