# Reference ("oracle") build of the original C++ HTN planner.
#
# It compiles the original framework, integration layer and HTNTranslator from
# a checkout of https://github.com/urosidoki/htn_planner (HTN_ORIGINAL_ROOT),
# translates every planner variant listed in testdata/planners.txt into C and
# links them into htn-oracle, a scenario runner whose output is the golden
# reference for the Go and Nim ports (testdata/scenarios/*.golden).
#
#   cmake -S tools/oracle -B build/oracle -G Ninja -DCMAKE_BUILD_TYPE=Release \
#         -DHTN_ORIGINAL_ROOT=/path/to/htn_planner
#   cmake --build build/oracle
cmake_minimum_required(VERSION 3.25)
project(HTNOracle LANGUAGES C CXX)

set(HTN_ORIGINAL_ROOT "" CACHE PATH "Checkout of github.com/urosidoki/htn_planner")
if(NOT EXISTS "${HTN_ORIGINAL_ROOT}/HTNFramework/src/HTNPlanner.h")
    message(FATAL_ERROR "Set HTN_ORIGINAL_ROOT to a checkout of github.com/urosidoki/htn_planner")
endif()
get_filename_component(PORT_ROOT "${CMAKE_CURRENT_LIST_DIR}/../.." ABSOLUTE)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE Release)
endif()

file(GLOB_RECURSE HTN_SOURCES CONFIGURE_DEPENDS "${HTN_ORIGINAL_ROOT}/HTNFramework/src/*.cpp")
list(FILTER HTN_SOURCES EXCLUDE REGEX "/pch\\.cpp$")
add_library(HTNFramework STATIC ${HTN_SOURCES})
target_include_directories(HTNFramework PUBLIC "${HTN_ORIGINAL_ROOT}/HTNFramework/src"
    PRIVATE "${HTN_ORIGINAL_ROOT}/ThirdParty/optick/src")
target_compile_definitions(HTNFramework PUBLIC HTN_RELEASE PRIVATE USE_OPTICK=0)
target_precompile_headers(HTNFramework PRIVATE "${HTN_ORIGINAL_ROOT}/HTNFramework/src/pch.h")
find_package(Threads REQUIRED)
target_link_libraries(HTNFramework PUBLIC Threads::Threads ${CMAKE_DL_LIBS})

file(GLOB_RECURSE HTN_INTEGRATION_SOURCES CONFIGURE_DEPENDS "${HTN_ORIGINAL_ROOT}/HTNIntegration/src/*.cpp")
add_library(HTNIntegration STATIC ${HTN_INTEGRATION_SOURCES})
target_include_directories(HTNIntegration PUBLIC "${HTN_ORIGINAL_ROOT}/HTNIntegration/src")
target_link_libraries(HTNIntegration PUBLIC HTNFramework)

add_executable(HTNTranslator "${HTN_ORIGINAL_ROOT}/HTNTranslator/src/main.cpp")
target_link_libraries(HTNTranslator PRIVATE HTNFramework)

# Translate every planner variant of the shared manifest.
set(GENERATED_ROOT "${CMAKE_CURRENT_BINARY_DIR}/generated")
set(GENERATED_SOURCES)
set(REGISTRY_DECLARATIONS "")
set(REGISTRY_ENTRIES "")
file(STRINGS "${PORT_ROOT}/testdata/planners.txt" MANIFEST_LINES)
set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS "${PORT_ROOT}/testdata/planners.txt")
foreach(LINE IN LISTS MANIFEST_LINES)
    string(STRIP "${LINE}" LINE)
    if(LINE STREQUAL "" OR LINE MATCHES "^#")
        continue()
    endif()
    separate_arguments(FIELDS UNIX_COMMAND "${LINE}")
    list(GET FIELDS 0 VARIANT)
    list(GET FIELDS 1 DOMAIN)
    list(GET FIELDS 2 ENTRY)
    set(OPTIONS)
    list(LENGTH FIELDS FIELD_COUNT)
    if(FIELD_COUNT GREATER 3)
        list(SUBLIST FIELDS 3 -1 OPTIONS)
    endif()
    get_filename_component(STEM "${DOMAIN}" NAME_WE)
    set(OUTPUT_DIRECTORY "${GENERATED_ROOT}/${VARIANT}")
    set(OUTPUT "${OUTPUT_DIRECTORY}/${STEM}.generated.c")
    file(GLOB_RECURSE DOMAIN_DEPENDENCIES CONFIGURE_DEPENDS "${PORT_ROOT}/Domains/*.domain")
    add_custom_command(OUTPUT "${OUTPUT}"
        COMMAND ${CMAKE_COMMAND} -E make_directory "${OUTPUT_DIRECTORY}"
        COMMAND HTNTranslator "${PORT_ROOT}/${DOMAIN}" "${ENTRY}" "${OUTPUT_DIRECTORY}" ${OPTIONS}
        WORKING_DIRECTORY "${PORT_ROOT}"
        DEPENDS HTNTranslator ${DOMAIN_DEPENDENCIES} "${PORT_ROOT}/testdata/planners.txt"
        COMMENT "Translating ${VARIANT}" VERBATIM)
    list(APPEND GENERATED_SOURCES "${OUTPUT}")
    string(APPEND REGISTRY_DECLARATIONS
        "extern \"C\" const HTNGeneratedPlannerDefinition* ${ENTRY}_GetDefinition(void);\n")
    string(APPEND REGISTRY_ENTRIES "    {\"${VARIANT}\", &${ENTRY}_GetDefinition},\n")
endforeach()
file(CONFIGURE OUTPUT "${CMAKE_CURRENT_BINARY_DIR}/OracleRegistry.inc"
    CONTENT "${REGISTRY_DECLARATIONS}\nconst OraclePlanner kOraclePlanners[] = {\n${REGISTRY_ENTRIES}};\n")

add_executable(htn-oracle "${CMAKE_CURRENT_LIST_DIR}/oracle.cpp" ${GENERATED_SOURCES})
target_include_directories(htn-oracle PRIVATE "${CMAKE_CURRENT_BINARY_DIR}")
target_link_libraries(htn-oracle PRIVATE HTNIntegration HTNFramework)
target_compile_definitions(htn-oracle PRIVATE HTN_PORT_ROOT="${PORT_ROOT}")
