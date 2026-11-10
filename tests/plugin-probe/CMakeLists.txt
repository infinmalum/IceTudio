cmake_minimum_required(VERSION 3.25)
project(IceTudioPluginProbe LANGUAGES CXX)

set(CLAP_ROOT "" CACHE PATH "Checkout of the official CLAP SDK")
set(VST3_ROOT "" CACHE PATH "Checkout of the official VST3 SDK with its hosting submodules")
if(NOT EXISTS "${CLAP_ROOT}/include/clap/clap.h" OR NOT EXISTS "${VST3_ROOT}/public.sdk/source/vst/hosting/module.h")
    message(FATAL_ERROR "Set CLAP_ROOT and VST3_ROOT to the pinned SDK checkouts")
endif()

set(SMTG_ENABLE_VST3_PLUGIN_EXAMPLES OFF CACHE BOOL "" FORCE)
set(SMTG_ENABLE_VST3_HOSTING_EXAMPLES OFF CACHE BOOL "" FORCE)
set(SMTG_ENABLE_VSTGUI_SUPPORT OFF CACHE BOOL "" FORCE)
add_subdirectory("${VST3_ROOT}" vst3 EXCLUDE_FROM_ALL)
set(hosting "${VST3_ROOT}/public.sdk/source/vst/hosting")
set(native "${CMAKE_CURRENT_SOURCE_DIR}/../../internal/pluginhost/native")
add_executable(icetudio-plugin-probe main.cpp clap.cpp vst3.cpp editor.cpp transport.cpp "${native}/editor.cpp"
    "${hosting}/module_linux.cpp"
    "${hosting}/plugprovider.cpp"
    "${VST3_ROOT}/public.sdk/source/common/memorystream.cpp")
target_include_directories(icetudio-plugin-probe PRIVATE "${CLAP_ROOT}/include" "${native}")
target_compile_features(icetudio-plugin-probe PRIVATE cxx_std_20)
find_package(X11 REQUIRED)
target_link_libraries(icetudio-plugin-probe PRIVATE X11::X11 sdk_hosting ${CMAKE_DL_LIBS})

add_library(scan-fault MODULE scan-fault.cpp)
set_target_properties(scan-fault PROPERTIES PREFIX "" SUFFIX ".clap")
add_library(scan-timeout MODULE scan-fault.cpp)
target_compile_definitions(scan-timeout PRIVATE ICE_SCAN_TIMEOUT)
set_target_properties(scan-timeout PROPERTIES PREFIX "" SUFFIX ".clap")

add_library(latency-fixture MODULE latency-fixture.cpp)
target_include_directories(latency-fixture PRIVATE "${CLAP_ROOT}/include")
target_compile_features(latency-fixture PRIVATE cxx_std_20)
set_target_properties(latency-fixture PROPERTIES PREFIX "" SUFFIX ".clap")

add_library(latency-vst3 MODULE latency-vst3.cpp
    "${VST3_ROOT}/public.sdk/source/main/linuxmain.cpp"
    "${VST3_ROOT}/public.sdk/source/vst/vstsinglecomponenteffect.cpp")
target_link_libraries(latency-vst3 PRIVATE sdk)
target_link_options(latency-vst3 PRIVATE "LINKER:-z,defs")
target_compile_features(latency-vst3 PRIVATE cxx_std_20)
set_target_properties(latency-vst3 PROPERTIES PREFIX ""
    LIBRARY_OUTPUT_DIRECTORY "${CMAKE_CURRENT_BINARY_DIR}/latency-vst3.vst3/Contents/${CMAKE_SYSTEM_PROCESSOR}-linux")
