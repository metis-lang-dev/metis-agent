// nemo-stream-latency — MODEL-EXPERIMENTS.md E2: feed a WAV corpus to the
// NeMo-Speech.cpp streaming recognizer the way a microphone would (20 ms
// frames, paced in real time unless -fast) and time, per utterance, the
// first partial and the final transcript relative to (a) the start of the
// audio and (b) the end of the audio — the second is the number the FSM
// feels: how long after the user stops does the text arrive.
//
// Build (after the cuda-asr preset built NeMo-Speech.cpp at $NEMO):
//   g++ -O2 -std=c++17 tools/nemo-stream-latency.cpp -I$NEMO/include \
//       -L$NEMO/build/cuda-asr/bin -lnemo_speech_asr_c -Wl,-rpath,$NEMO/build/cuda-asr/bin \
//       -o bin/nemo-stream-latency
// Run:
//   bin/nemo-stream-latency -model models/nemotron/…q8_0.gguf -manifest models/corpus-wav/manifest.tsv \
//       [-device cuda:0|cpu]  (default cuda:0) [-fast] [-tsv out.tsv]
// Output per row: wav, audio s, first-partial ms (from audio start), final
// ms from audio start, final ms after audio end, transcript. -tsv writes
// transcript<TAB>intent<TAB>source for llmbench -corpus (intent after ASR).
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <sstream>
#include <string>
#include <algorithm>
#include <thread>
#include <vector>

#include "nemo_speech/asr.h"

namespace {

struct Row { std::string wav, text, want, src; };

bool read_wav16k(const std::string& path, std::vector<float>& out) {
    std::ifstream f(path, std::ios::binary);
    if (!f) return false;
    std::vector<char> b((std::istreambuf_iterator<char>(f)), {});
    if (b.size() < 44 || std::memcmp(b.data(), "RIFF", 4) || std::memcmp(b.data() + 8, "WAVE", 4)) return false;
    size_t off = 12; uint32_t rate = 0; uint16_t ch = 0, bits = 0;
    while (off + 8 <= b.size()) {
        std::string id(b.data() + off, 4);
        uint32_t sz; std::memcpy(&sz, b.data() + off + 4, 4); off += 8;
        if (off + sz > b.size()) sz = b.size() - off;
        if (id == "fmt ") { std::memcpy(&ch, b.data() + off + 2, 2); std::memcpy(&rate, b.data() + off + 4, 4); std::memcpy(&bits, b.data() + off + 14, 2); }
        else if (id == "data") {
            if (rate != 16000 || ch != 1 || bits != 16) return false;
            out.resize(sz / 2);
            for (size_t i = 0; i < out.size(); i++) { int16_t s; std::memcpy(&s, b.data() + off + 2 * i, 2); out[i] = s / 32768.0f; }
            return true;
        }
        off += sz + (sz & 1);
    }
    return false;
}

std::vector<Row> read_manifest(const std::string& path) {
    std::vector<Row> rows; std::ifstream f(path); std::string ln;
    while (std::getline(f, ln)) {
        if (ln.empty() || ln[0] == '#') continue;
        std::vector<std::string> p; std::stringstream ss(ln); std::string c;
        while (std::getline(ss, c, '\t')) p.push_back(c);
        if (p.size() < 2) continue;
        rows.push_back({p[0], p[1], p.size() > 2 ? p[2] : "", p.size() > 3 ? p[3] : "corpus"});
    }
    return rows;
}

std::string dir_of(const std::string& p) { auto i = p.rfind('/'); return i == std::string::npos ? "." : p.substr(0, i); }

using clk = std::chrono::steady_clock;
double ms_since(clk::time_point t) { return std::chrono::duration<double, std::milli>(clk::now() - t).count(); }

}  // namespace

int main(int argc, char** argv) {
    std::string model, manifest = "models/corpus-wav/manifest.tsv", device, tsv; bool fast = false;
    for (int i = 1; i < argc; i++) {
        std::string a = argv[i];
        auto next = [&]() { return i + 1 < argc ? std::string(argv[++i]) : std::string(); };
        if (a == "-model") model = next(); else if (a == "-manifest") manifest = next();
        else if (a == "-device") device = next(); else if (a == "-tsv") tsv = next(); else if (a == "-fast") fast = true;
        else { std::fprintf(stderr, "unknown arg %s\n", a.c_str()); return 2; }
    }
    if (model.empty()) { std::fprintf(stderr, "-model required\n"); return 2; }

    nemo_speech_asr_model_config mc{}; mc.size = sizeof mc; mc.path = model.c_str();
    nemo_speech_asr_backend_config bc{}; bc.size = sizeof bc; bc.gpu = 0;  // default: first GPU
    if (device == "cpu") bc.gpu = -1;
    else if (device.rfind("cuda:", 0) == 0) bc.gpu = std::atoi(device.c_str() + 5);
    nemo_speech_asr_recognizer_config rc{}; rc.size = sizeof rc; rc.backend = &bc; rc.model = &mc;
    nemo_speech_asr_recognizer* rec = nullptr;
    auto t_load = clk::now();
    if (nemo_speech_asr_create(&rc, &rec) != NEMO_SPEECH_ASR_OK) { std::fprintf(stderr, "create: %s\n", nemo_speech_asr_last_error()); return 1; }
    std::fprintf(stderr, "loaded %s on %s in %.0f ms\n", model.c_str(), device.empty() ? "auto" : device.c_str(), ms_since(t_load));

    nemo_speech_asr_recognition_options opts = nemo_speech_asr_recognition_options_default();
    opts.interim_results = true;

    auto rows = read_manifest(manifest);
    std::string base = dir_of(manifest);
    FILE* out = tsv.empty() ? nullptr : std::fopen(tsv.c_str(), "w");
    if (out) std::fprintf(out, "# nemotron streaming transcripts of %s — utterance\texpected_intent\tsource\n", manifest.c_str());

    // warm-up: one silent second through a stream, so the first row is not the CUDA init
    {
        std::vector<float> z(16000, 0.f); nemo_speech_asr_stream* s = nullptr;
        if (nemo_speech_asr_streaming_recognize(rec, &opts, &s) == NEMO_SPEECH_ASR_OK) {
            nemo_speech_asr_stream_push_f32(s, z.data(), z.size(), 16000); nemo_speech_asr_stream_finish(s);
            nemo_speech_asr_result* r = nullptr;
            while (nemo_speech_asr_stream_next(s, &r) == NEMO_SPEECH_ASR_OK && r) nemo_speech_asr_result_destroy(r);
            nemo_speech_asr_stream_close(s);
        }
    }

    const size_t frame = 320;  // 20 ms @ 16 kHz, the harness's mic frame
    std::vector<double> firsts, finals_start, finals_end; int n = 0;
    std::printf("%-44s %7s %9s %9s %10s  %s\n", "wav", "audio_s", "partial", "final", "final-end", "transcript");
    for (const auto& row : rows) {
        std::vector<float> pcm;
        if (!read_wav16k(base + "/" + row.wav, pcm)) { std::fprintf(stderr, "skip %s (not 16k mono s16)\n", row.wav.c_str()); continue; }
        double audio_s = pcm.size() / 16000.0;
        nemo_speech_asr_stream* s = nullptr;
        if (nemo_speech_asr_streaming_recognize(rec, &opts, &s) != NEMO_SPEECH_ASR_OK) { std::fprintf(stderr, "stream: %s\n", nemo_speech_asr_last_error()); return 1; }
        auto t0 = clk::now(); double first = -1, fin = -1; std::string text, last;
        auto pump = [&]() {
            nemo_speech_asr_result* r = nullptr;
            while (nemo_speech_asr_stream_next(s, &r) == NEMO_SPEECH_ASR_OK && r) {
                const char* t = nemo_speech_asr_result_transcript(r, 0);
                std::string cur = t ? t : "";
                if (!cur.empty() && first < 0) first = ms_since(t0);
                if (nemo_speech_asr_result_is_final(r)) { fin = ms_since(t0); if (!cur.empty()) text = cur; }
                else if (!cur.empty()) last = cur;
                nemo_speech_asr_result_destroy(r);
            }
        };
        for (size_t off = 0; off < pcm.size(); off += frame) {
            size_t n_s = std::min(frame, pcm.size() - off);
            if (!fast) {  // pace to the wall clock: this frame is "recorded" at off/16000 s
                auto due = t0 + std::chrono::microseconds(static_cast<long long>(off * 1e6 / 16000));
                std::this_thread::sleep_until(due);
            }
            nemo_speech_asr_stream_push_f32(s, pcm.data() + off, n_s, 16000);
            pump();
        }
        double t_end = ms_since(t0);  // audio ended (≈ audio_s*1000 when paced)
        nemo_speech_asr_stream_finish(s);
        pump();
        nemo_speech_asr_stream_close(s);
        if (text.empty()) text = last;
        if (fin < 0) fin = ms_since(t0);
        n++; firsts.push_back(first); finals_start.push_back(fin); finals_end.push_back(fin - t_end);
        std::printf("%-44s %7.2f %7.0fms %7.0fms %8.0fms  %s\n", row.wav.c_str(), audio_s, first, fin, fin - t_end, text.c_str());
        if (out) std::fprintf(out, "%s\t%s\t%s\n", text.empty() ? "(silence)" : text.c_str(), row.want.c_str(), row.src.c_str());
    }
    auto stat = [](std::vector<double> v, const char* name) {
        if (v.empty()) return; std::sort(v.begin(), v.end()); double sum = 0; for (double x : v) sum += x;
        std::printf("  %-10s mean %.0f  p50 %.0f  p90 %.0f  max %.0f ms\n", name, sum / v.size(), v[v.size() / 2], v[v.size() * 9 / 10], v.back());
    };
    std::printf("\n== nemotron streaming, %s, %s pacing, n=%d\n", device.empty() ? "auto" : device.c_str(), fast ? "no" : "real-time", n);
    stat(firsts, "partial"); stat(finals_start, "final"); stat(finals_end, "final-end");
    if (out) std::fclose(out);
    nemo_speech_asr_destroy(rec);
    return 0;
}
