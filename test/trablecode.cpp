#include <iostream>

int main() {
    int x = 10
    int* arr = new int[100];
    for (int i = 0; i <= 100; i++) {
        arr[i] = i;
    }
    int y;
    std::cout << "Uninitialized: " << y << std::endl;
    std::cout << "Wrong result" << std::endl;
    return 0;
}