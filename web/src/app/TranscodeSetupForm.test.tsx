import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import type { components } from "@/api/schema";

import { qualityLabel, SetupWarnings, TranscodeSetupForm } from "./TranscodeSetupForm";

const fetchMock = vi.hoisted(() => vi.fn());

vi.mock("@/api/client", async () => {
  const { default: createClient } = await import("openapi-fetch");
  return {
    api: createClient({ baseUrl: "http://solarr.test/api/v1", fetch: fetchMock }),
    ok: async <T,>(call: Promise<{ data?: T }>) => (await call).data as T,
  };
});

type Setup = components["schemas"]["TranscodeSetup"];

const setup = (rateMode: string): Setup => ({
  version: 1,
  encode: { engine: "auto", allow_cpu_fallback: true, rate_mode: rateMode as "quality", quality: 28, hardware_quality_offset: 0, video_bitrate_kbps: 4500, preset: "slow" },
  skip: { transcoded_before: true, container_matches: true, codec_matches: true, also_video_codecs: [], also_audio_codecs: [], resolution_at_or_below: true, rate_at_or_below: true, only_wanted_languages: true, within_range: true, range_pct: 20, below_max: true, max_pct: 120 },
  streams: { copy_video_on_target: true, video_copy_max_pct: 120, copy_audio_on_target: true, audio_copy_max_pct: 120, downscale: true, no_upscale: true, reduce_fps: false, no_fps_increase: true, downmix: false, no_upmix: true, no_sample_rate_increase: true, remove_unwanted_audio: false, remove_unwanted_subtitles: false },
  video_filters: { deinterlace: false, denoise: "off", denoise_method: "", deblock: "off", deband: "off", deflicker: "off", sharpen: "off", sharpen_method: "", tonemap: false },
  audio_filters: { normalize: false, dialog_enhance: false, compress: false, denoise: "off", denoise_method: "afftdn", declip: false },
  check: { duration: true, duration_seconds: 2, smaller: true, within_range: true, range_pct: 20, below_max: true, max_pct: 120 },
  ladder: { extra_passes: 1, quality_step: 2, bitrate_step_pct: 15 },
});

function renderForm(rateMode: string) {
  const state = {
    target_id: 7, is_default: true, revision: 0, setup: setup(rateMode), warnings: [{ code: "checks_off" }],
    availability: [
      { engine: "auto", available: true, lookahead: true, ten_bit: true, quality_mode: "crf", preset_applies: true },
      { engine: "nvenc", available: false, reason: "not present on this host", lookahead: false, ten_bit: false, quality_mode: "", preset_applies: true },
    ],
    filters: {},
  };
  fetchMock.mockImplementation((request: Request) =>
    Promise.resolve(new Response(JSON.stringify(request.url.endsWith("/qualitytargets") ? [{ id: 7, name: "Films", media_type: "video", audio_codec: "aac", container: "mkv", tolerance_pct: 20 }] : state), { status: 200, headers: { "Content-Type": "application/json" } })),
  );
  const root = createRootRoute();
  const route = createRoute({ getParentRoute: () => root, path: "/settings/transcode/targets/$targetId", component: () => <TranscodeSetupForm targetId={7} /> });
  const router = createRouter({ routeTree: root.addChildren([route]), history: createMemoryHistory({ initialEntries: ["/settings/transcode/targets/7"] }) });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("an engine that cannot encode the codec is disabled and says why", async () => {
  renderForm("quality");
  const radio = (await screen.findByLabelText(/NVIDIA NVENC/)) as HTMLInputElement;
  expect(radio.disabled).toBe(true);
  expect(screen.getByText("not present on this host")).toBeTruthy();
});

test("quality mode shows the quality number and bitrate mode shows the bitrate instead", async () => {
  renderForm("quality");
  await screen.findByLabelText("CRF");
  expect(screen.queryByLabelText(/Video bitrate/)).toBeNull();
  fireEvent.change(screen.getByLabelText("Rate control"), { target: { value: "bitrate" } });
  expect(screen.getByLabelText(/Video bitrate/)).toBeTruthy();
  expect(screen.queryByLabelText("CRF")).toBeNull();
});

test("each warning code renders its sentence and the quality label follows the engine", () => {
  render(<SetupWarnings warnings={[{ code: "checks_off" }, { code: "range_unknown" }, { code: "quality_differs_from_target", value: "30" }]} />);
  expect(screen.getByRole("alert").textContent).toContain("Both result checks are off");
  expect(screen.getByRole("alert").textContent).toContain("names no bitrate");
  expect(screen.getByRole("alert").textContent).toContain("quality (30)");
  const availability = [{ engine: "qsv", available: true, lookahead: true, ten_bit: true, quality_mode: "la_icq", preset_applies: true }] as const;
  expect(qualityLabel("qsv", [...availability])).toBe("ICQ (global quality)");
  expect(qualityLabel("software", [...availability])).toBe("CRF");
});
