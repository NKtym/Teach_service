#include <iostream>

int main() {
    int* arr = new int[100];
    for (int i = 0; i < 100; i++) {
        arr[i] = i;
    }

    int y = 1;
    std::cout << "Initialized: " << y << std::endl;
    std::cout << "OK" << std::endl;

    delete[] arr;
    return 0;
}