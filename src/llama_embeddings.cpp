// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

#include "llama_embeddings.h"
#include "common.h"
#include "llama.h"
#include "ggml.h"
#include <vector>
#include <string>
#include <cstring> 

static common_params g_params;
static llama_model* g_model = nullptr;
static llama_context* g_ctx = nullptr;

static common_init_result_ptr g_init_result = nullptr;

static bool g_initialized = false;
static void* g_copied_buffer = nullptr;
static size_t g_copied_size = 0;

#ifdef _WIN32
void llama_copy_memory_buffer(const void* buf, size_t size) { (void)buf; (void)size; }
#else
extern "C" {
    typedef struct {
        const void * buf;
        size_t       size;
    } ggml_memory_file_t;
    
    extern ggml_memory_file_t g_memory_file;
    void ggml_set_memory_buffer(const void * buf, size_t size);
}

void llama_copy_memory_buffer(const void* buf, size_t size) {
    if (g_copied_buffer) {
        free(g_copied_buffer);
    }
    
    g_copied_buffer = malloc(size);
    if (g_copied_buffer) {
        memcpy(g_copied_buffer, buf, size);
        g_copied_size = size;
        ggml_set_memory_buffer(g_copied_buffer, size);
    } else {
        fprintf(stderr, "Failed to allocate memory for model copy\n");
    }
}
#endif

void silent_log_callback(ggml_log_level level, const char * text, void * user_data) {
    (void)level;
    (void)text;
    (void)user_data;
}

int llama_embeddings_init(const char* model_path, int n_threads) {
    llama_log_set(silent_log_callback, NULL);

    if (g_initialized) {
        return 0;
    }

    #if defined(_WIN32)
        ggml_backend_load_all();
    #else
    if (strcmp(model_path, "memory:") == 0) {
        if (g_memory_file.buf == nullptr) {
            fprintf(stderr, "Error: 'memory:' path specified but buffer not set. Call llama_copy_memory_buffer first.\n");
            return 1;
        }
    }
    #endif

    common_params temp_params = {};
    temp_params.model.path = model_path;
    temp_params.embedding = true;
    temp_params.embd_normalize = 2;
    temp_params.warmup = false;
    
    if (n_threads <= 0) n_threads = 1;
    temp_params.cpuparams.n_threads = n_threads;
    temp_params.cpuparams_batch.n_threads = n_threads;

    temp_params.n_ctx = 512;
    temp_params.n_batch = 512;
    temp_params.n_gpu_layers = 0;
    temp_params.use_mmap = false;

    auto llama_init = common_init_from_params(temp_params);
    
    if (!llama_init) {
        llama_log_set(NULL, NULL);
        fprintf(stderr, "Error: Failed to initialize model from '%s' (pass 1)\n", model_path);
        return 1;
    }

    llama_model* temp_model = llama_init->model();
    
    if (temp_model == nullptr) {
        llama_log_set(NULL, NULL);
        fprintf(stderr, "Error: Failed to obtain model pointer (pass 1)\n");
        return 1;
    }

    llama_init->free_context(); 

    const int model_ctx_size = llama_model_n_ctx_train(temp_model);
    
    llama_init.reset(); 

    common_params final_params = {};
    final_params.model.path = model_path;
    final_params.embedding = true;
    final_params.embd_normalize = 2;
    final_params.warmup = false;
    final_params.cpuparams.n_threads = n_threads;
    final_params.cpuparams_batch.n_threads = n_threads;
    final_params.n_ctx = model_ctx_size;
    
    final_params.n_batch = model_ctx_size;
    final_params.n_ubatch = model_ctx_size;

    final_params.n_gpu_layers = 0;
    final_params.use_mmap = false;

    auto llama_init_final = common_init_from_params(final_params);
    
    if (!llama_init_final) {
        llama_log_set(NULL, NULL);
        fprintf(stderr, "Error: Failed to initialize model or context from '%s' (pass 2)\n", model_path);
        return 1;
    }

    g_model = llama_init_final->model();
    g_ctx = llama_init_final->context();

    if (g_model == nullptr || g_ctx == nullptr) {
        llama_log_set(NULL, NULL);
        fprintf(stderr, "Error: Failed to get model or context pointers (pass 2)\n");
        return 1;
    }

    g_init_result = std::move(llama_init_final);

    g_params = final_params;
    g_initialized = true;
    return 0;
}

float* llama_embeddings_get(const char* text, int* n_embd_out) {
    if (!g_initialized || !text || n_embd_out == NULL) {
        if (n_embd_out) *n_embd_out = 0;
        return NULL;
    }

    const int max_context_tokens = llama_n_ctx(g_ctx);
    
    std::vector<llama_token> tokens = common_tokenize(g_ctx, text, true, true);
    if (tokens.empty()) {
        *n_embd_out = 0;
        return NULL;
    }

    if ((int)tokens.size() > max_context_tokens) {
        fprintf(stderr, "Warning: Input text exceeds maximum context length (%d tokens). Truncating to %d tokens.\n", 
                (int)tokens.size(), max_context_tokens);
        tokens.resize(max_context_tokens);
    }

    if ((int)tokens.size() > g_params.n_batch) {
        fprintf(stderr, "Error: Token count (%zu) exceeds batch size (%d). This shouldn't happen after truncation.\n", 
                tokens.size(), g_params.n_batch);
        *n_embd_out = 0;
        return NULL;
    }

    llama_batch batch = llama_batch_init(tokens.size(), 0, 1);
    for (size_t i = 0; i < tokens.size(); ++i) {
        common_batch_add(batch, tokens[i], i, { 0 }, true);
    }

    llama_memory_clear(llama_get_memory(g_ctx), true);

    if (llama_decode(g_ctx, batch) < 0) {
        fprintf(stderr, "Error: llama_decode failed\n");
        llama_batch_free(batch);
        *n_embd_out = 0;
        return NULL;
    }

    enum llama_pooling_type pooling_type = llama_pooling_type(g_ctx);
    if (pooling_type == LLAMA_POOLING_TYPE_NONE) {
        fprintf(stderr, "Warning: model does not have a pooling type. This wrapper requires a pooling model.\n");
        llama_batch_free(batch);
        *n_embd_out = 0;
        return NULL;
    }

    const int n_embd = llama_model_n_embd(g_model);
    float* output = (float*)malloc(n_embd * sizeof(float));
    if (!output) {
        fprintf(stderr, "Error: Failed to allocate memory\n");
        llama_batch_free(batch);
        *n_embd_out = 0;
        return NULL;
    }

    const float* embd_in = llama_get_embeddings_seq(g_ctx, 0);
    if (embd_in == nullptr) {
        fprintf(stderr, "Error: failed to get sequence embeddings\n");
        free(output);
        llama_batch_free(batch);
        *n_embd_out = 0;
        return nullptr;
    }

    common_embd_normalize(embd_in, output, n_embd, g_params.embd_normalize);

    llama_batch_free(batch);
    *n_embd_out = 1;
    return output;
}

void llama_embeddings_free_output(float* output) {
    if (output) {
        free(output);
    }
}

void llama_embeddings_free(void) {
    if (g_initialized) {
        g_init_result.reset(); 
        
        g_model = NULL;
        g_ctx = NULL;
        
        llama_backend_free();
        g_initialized = false;
    }
}

int llama_embeddings_get_dimension(void) {
    if (!g_initialized) return -1;
    return llama_model_n_embd(g_model);
}
