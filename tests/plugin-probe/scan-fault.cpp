// Deliberately fault at dlopen time to verify the external scanner boundary.
#include <cstdlib>
#include <unistd.h>

__attribute__((constructor)) static void fault_on_load() {
#ifdef ICE_SCAN_TIMEOUT
    sleep(60);
#else
    std::abort();
#endif
}
